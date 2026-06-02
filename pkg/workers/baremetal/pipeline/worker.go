// Package worker is a long-running process that polls the piper master and executes tasks.
// Execution logic is delegated to pkg/runner.
package pipelineworker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/piper/piper/pkg/event"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

// Config holds Worker configuration.
type Config struct {
	MasterURL           string
	Label               string
	Token               string
	Version             string
	Capabilities        []string
	PollInterval        time.Duration
	ShutdownGracePeriod time.Duration
	Concurrency         int
	OutputDir           string
	GitUser             string
	GitToken            string
	// StorageURL selects the artifact store backend.
	// Supported schemes: s3://, file://, http://, https://
	StorageURL string
}

// Worker polls the master and executes tasks.
type Worker struct {
	cfg    Config
	id     string // unique worker ID (UUID)
	runner *runner.Runner
	poller *http.Client

	mu       sync.Mutex
	inFlight int
	wg       sync.WaitGroup
	cancels  map[string]trackedCancel
}

type trackedCancel struct {
	runID  string
	cancel context.CancelFunc
}

func New(cfg Config) (*Worker, error) {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 3 * time.Second
	}
	if cfg.Concurrency < 0 {
		return nil, fmt.Errorf("worker: concurrency must be >= 0, got %d", cfg.Concurrency)
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 4
	}
	if cfg.ShutdownGracePeriod == 0 {
		cfg.ShutdownGracePeriod = 30 * time.Second
	}

	r, err := runner.New(runner.Config{
		MasterURL:  cfg.MasterURL,
		Token:      cfg.Token,
		OutputDir:  cfg.OutputDir,
		GitUser:    cfg.GitUser,
		GitToken:   cfg.GitToken,
		StorageURL: cfg.StorageURL,
	})
	if err != nil {
		return nil, err
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	return &Worker{
		cfg:     cfg,
		id:      uuid.New().String(),
		runner:  r,
		poller:  &http.Client{Timeout: 10 * time.Second},
		cancels: make(map[string]trackedCancel),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	// Register with master
	if err := w.register(ctx); err != nil {
		slog.Warn("worker registration failed, continuing without registration", "err", err)
	}

	slog.Info("worker started",
		"id", w.id,
		"master", w.cfg.MasterURL,
		"label", w.cfg.Label,
		"concurrency", w.cfg.Concurrency,
		"poll_interval", w.cfg.PollInterval,
	)

	// heartbeat goroutine
	go w.heartbeatLoop(ctx)
	go w.watchCancelEvents(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker shutting down", "id", w.id)
			return w.waitForDrain()
		default:
		}

		if !w.available() {
			w.sleep(ctx)
			continue
		}

		task, err := poll(ctx, w.poller, w.cfg, w.id)
		if err != nil {
			slog.Warn("poll error", "err", err)
			w.sleep(ctx)
			continue
		}
		if task == nil {
			w.sleep(ctx)
			continue
		}

		// Register cancel before launching the goroutine so that a run.canceled
		// SSE event arriving between poll() and goroutine start is not dropped.
		taskCtx, cancel := context.WithCancel(context.Background())
		w.trackCancel(task.ID, task.RunID, cancel)

		w.mu.Lock()
		w.inFlight++
		w.mu.Unlock()
		w.wg.Add(1)

		go func(t *proto.Task, ctx context.Context, c context.CancelFunc) {
			defer func() {
				w.untrackCancel(t.ID)
				c()
				w.mu.Lock()
				w.inFlight--
				w.mu.Unlock()
				w.wg.Done()
			}()
			w.runner.Run(ctx, t)
		}(task, taskCtx, cancel)
	}
}

func (w *Worker) trackCancel(taskID, runID string, cancel context.CancelFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancels[taskID] = trackedCancel{runID: runID, cancel: cancel}
}

func (w *Worker) untrackCancel(taskID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.cancels, taskID)
}

func (w *Worker) cancelInFlight() {
	w.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(w.cancels))
	for _, tracked := range w.cancels {
		cancels = append(cancels, tracked.cancel)
	}
	w.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (w *Worker) waitForDrain() error {
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(w.cfg.ShutdownGracePeriod):
		slog.Warn("worker shutdown grace period exceeded; canceling in-flight tasks", "id", w.id, "grace_period", w.cfg.ShutdownGracePeriod)
		w.cancelInFlight()
		<-done
		return nil
	}
}

func (w *Worker) cancelRun(runID string) {
	w.mu.Lock()
	cancels := make([]context.CancelFunc, 0)
	for _, tracked := range w.cancels {
		if tracked.runID == runID {
			cancels = append(cancels, tracked.cancel)
		}
	}
	w.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (w *Worker) watchCancelEvents(ctx context.Context) {
	if w.cfg.MasterURL == "" {
		return
	}
	for {
		if err := w.consumeCancelEvents(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("cancel event stream failed", "err", err)
		}
		w.sleep(ctx)
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Worker) consumeCancelEvents(ctx context.Context) error {
	url := strings.TrimRight(w.cfg.MasterURL, "/") + "/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if w.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("events: unexpected status %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	data := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			w.handleCancelEvent(eventType, data)
			eventType = ""
			data = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data != "" {
				data += "\n"
			}
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return scanner.Err()
}

func (w *Worker) handleCancelEvent(eventType, data string) {
	if eventType != "run.canceled" || data == "" {
		return
	}
	var ev event.Event
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		slog.Warn("cancel event decode failed", "err", err)
		return
	}
	runID, _ := ev.Fields["run_id"].(string)
	if runID == "" {
		return
	}
	slog.Info("run cancellation received", "run_id", runID)
	w.cancelRun(runID)
}

// register registers this worker with the master.
func (w *Worker) register(ctx context.Context) error {
	if w.cfg.MasterURL == "" {
		return nil
	}
	hostname, _ := os.Hostname()
	body := map[string]any{
		"id":           w.id,
		"label":        w.cfg.Label,
		"version":      w.cfg.Version,
		"capabilities": strings.Join(w.cfg.Capabilities, ","),
		"concurrency":  w.cfg.Concurrency,
		"hostname":     hostname,
	}
	data, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/api/workers", w.cfg.MasterURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	}
	resp, err := w.poller.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// heartbeatLoop sends a keepalive signal to the master every 10 seconds.
// On a 404 (unregistered) heartbeat response, it attempts re-registration.
func (w *Worker) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.heartbeat(ctx); err != nil {
				slog.Warn("heartbeat failed, re-registering", "err", err)
				_ = w.register(ctx)
			}
		}
	}
}

func (w *Worker) heartbeat(ctx context.Context) error {
	if w.cfg.MasterURL == "" {
		return nil
	}

	w.mu.Lock()
	inFlight := w.inFlight
	w.mu.Unlock()

	body, _ := json.Marshal(map[string]any{"in_flight": inFlight})
	url := fmt.Sprintf("%s/api/workers/%s/heartbeat", w.cfg.MasterURL, w.id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	}
	resp, err := w.poller.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not registered")
	}
	return nil
}

func (w *Worker) available() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inFlight < w.cfg.Concurrency
}

func (w *Worker) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(w.cfg.PollInterval):
	}
}
