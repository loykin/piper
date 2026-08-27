package alerting

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"text/template"
	"time"

	"github.com/loykin/piper/internal/event"
	pkgalerting "github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notify"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
)

const (
	deliveryQueueSize = 256
	deliveryWorkers   = 4
)

type CredentialResolver interface {
	ResolveNotification(ctx context.Context, projectID, name string) (credential.NotificationCredential, error)
}

type Engine struct {
	repo        pkgalerting.Repository
	credentials CredentialResolver

	mu           sync.RWMutex
	rules        map[string][]*pkgalerting.Rule
	jobs         chan deliveryJob
	openNotifier func(string, map[string]string) (notify.Notifier, error)
}

type deliveryJob struct {
	rule          *pkgalerting.Rule
	projectID     string
	credentialRef string
	event         event.Event
	message       *notify.Message
}

func NewEngine(repo pkgalerting.Repository, credentials CredentialResolver) *Engine {
	return &Engine{repo: repo, credentials: credentials, rules: make(map[string][]*pkgalerting.Rule), jobs: make(chan deliveryJob, deliveryQueueSize), openNotifier: func(kind string, data map[string]string) (notify.Notifier, error) {
		return notify.Open(kind, data, nil)
	}}
}

func (e *Engine) Refresh(ctx context.Context) error {
	rules, err := e.repo.ListEnabled(ctx)
	if err != nil {
		return err
	}
	next := make(map[string][]*pkgalerting.Rule)
	for _, rule := range rules {
		next[rule.ProjectID] = append(next[rule.ProjectID], rule)
	}
	e.mu.Lock()
	e.rules = next
	e.mu.Unlock()
	return nil
}

func (e *Engine) Start(ctx context.Context, bus event.Bus) <-chan struct{} {
	done := make(chan struct{})
	events, cancel := bus.Subscribe()
	go func() {
		defer close(done)
		defer cancel()
		e.run(ctx, events)
	}()
	return done
}

func (e *Engine) run(ctx context.Context, events <-chan event.Event) {
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range deliveryWorkers {
		wg.Add(1)
		go func() { defer wg.Done(); e.deliveryWorker(workerCtx) }()
	}
	defer func() {
		cancelWorkers()
		wg.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case value, ok := <-events:
			if !ok {
				return
			}
			e.evaluate(ctx, normalizeEvent(value))
		}
	}
}

func normalizeEvent(value event.Event) event.Event {
	if value.Fields == nil {
		value.Fields = make(map[string]any)
	}
	switch value.Type {
	case "notebook.running":
		value.Fields["status"] = "running"
	case "notebook.stopped":
		value.Fields["status"] = "stopped"
	case "notebook.failed":
		value.Fields["status"] = "failed"
	}
	return value
}

func (e *Engine) evaluate(ctx context.Context, value event.Event) {
	e.mu.RLock()
	rules := append([]*pkgalerting.Rule(nil), e.rules[value.ProjectID]...)
	e.mu.RUnlock()
	for _, rule := range rules {
		matched := pkgalerting.MatchEvent(rule, value.Type, value.Fields)
		if rule.Source == pkgalerting.SourceMetric && value.Type == "metric.recorded" {
			key, _ := value.Fields["key"].(string)
			metric, ok := number(value.Fields["value"])
			matched = ok && pkgalerting.MatchMetric(rule, key, metric)
		}
		if !matched {
			continue
		}
		now := time.Now().UTC()
		before := now.Add(-time.Duration(rule.CooldownSeconds) * time.Second)
		claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		claimed, err := e.repo.TryClaimFire(claimCtx, rule.ProjectID, rule.ID, now, before)
		cancel()
		if err != nil {
			slog.Error("alerting: claim rule failed", "rule_id", rule.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		for _, ref := range rule.Notify {
			select {
			case e.jobs <- deliveryJob{rule: rule, projectID: rule.ProjectID, credentialRef: ref, event: value}:
			default:
				msg := "notification delivery queue is full"
				e.recordDelivery(rule.ProjectID, rule.ID, time.Now().UTC(), false, msg)
				slog.Warn("alerting: delivery dropped", "rule_id", rule.ID, "credential_ref", ref)
			}
		}
	}
}

func (e *Engine) deliveryWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-e.jobs:
			e.deliver(ctx, job)
		}
	}
}

func (e *Engine) deliver(ctx context.Context, job deliveryJob) {
	resolved, err := e.credentials.ResolveNotification(ctx, job.projectID, job.credentialRef)
	if err == nil {
		var notifier notify.Notifier
		notifier, err = e.openNotifier(string(resolved.Kind), resolved.Data)
		var message notify.Message
		if job.message != nil {
			message = *job.message
		} else {
			message = messageFor(job.rule, job.event)
		}
		if err == nil {
			err = notifier.Send(ctx, message)
		}
	}
	now := time.Now().UTC()
	if err != nil {
		message := err.Error()
		if len(message) > 512 {
			message = message[:512]
		}
		if job.rule != nil {
			e.recordDelivery(job.projectID, job.rule.ID, now, false, message)
		}
		slog.Warn("alerting: delivery failed", "project_id", job.projectID, "credential_ref", job.credentialRef, "err", message)
		return
	}
	if job.rule != nil {
		e.recordDelivery(job.projectID, job.rule.ID, now, true, "")
	}
}

func (e *Engine) recordDelivery(projectID, ruleID string, at time.Time, success bool, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.repo.RecordDelivery(ctx, projectID, ruleID, at, success, message); err != nil {
		slog.Warn("alerting: record delivery failed", "rule_id", ruleID, "err", err)
	}
}

func messageFor(rule *pkgalerting.Rule, value event.Event) notify.Message {
	title := fmt.Sprintf("Piper alert: %s", rule.Name)
	body := fmt.Sprintf("%s matched for project %s", value.Type, value.ProjectID)
	return notify.Message{Title: title, Body: body, Fields: map[string]any{"event_id": value.ID, "event_type": value.Type, "project_id": value.ProjectID, "at": value.At, "event": value.Fields}}
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func (e *Engine) NotifyPipelineOutcome(ctx context.Context, projectID, runID, status string, pl *pipeline.Pipeline) {
	var outcome *pipeline.OnOutcome
	if status == run.StatusSuccess {
		outcome = pl.Spec.OnSuccess
	} else if status == run.StatusFailed {
		outcome = pl.Spec.OnFailure
	}
	if outcome == nil {
		return
	}
	data := map[string]any{"ProjectID": projectID, "RunID": runID, "PipelineName": pl.Metadata.Name, "Status": status}
	for _, action := range outcome.Notify {
		body := action.Message
		if body == "" {
			body = "Pipeline {{.PipelineName}} run {{.RunID}} finished with status {{.Status}}."
		}
		var rendered bytes.Buffer
		tmpl, err := template.New("message").Option("missingkey=error").Parse(body)
		if err == nil {
			err = tmpl.Execute(&rendered, data)
		}
		if err != nil {
			slog.Warn("alerting: pipeline notification template failed", "run_id", runID, "err", err)
			continue
		}
		message := notify.Message{Title: fmt.Sprintf("Piper pipeline %s: %s", status, pl.Metadata.Name), Body: rendered.String(), Fields: map[string]any{"project_id": projectID, "run_id": runID, "pipeline_name": pl.Metadata.Name, "status": status}}
		select {
		case e.jobs <- deliveryJob{projectID: projectID, credentialRef: action.CredentialRef, message: &message}:
		default:
			slog.Warn("alerting: pipeline notification dropped because delivery queue is full", "run_id", runID, "credential_ref", action.CredentialRef)
		}
	}
}
