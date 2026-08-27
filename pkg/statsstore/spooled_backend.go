package statsstore

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type spooledBackend struct {
	logs                    LogBackend
	metrics                 MetricBackend
	spool                   *diskSpool
	spoolLogs, spoolMetrics bool

	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	wg         sync.WaitGroup
	deliveryMu sync.Mutex

	mu      sync.RWMutex
	lastErr error
	errGen  uint64
}

func newSpooledBackend(logs LogBackend, metrics MetricBackend, spool *diskSpool, spoolLogs, spoolMetrics bool) *spooledBackend {
	ctx, cancel := context.WithCancel(context.Background())
	b := &spooledBackend{logs: logs, metrics: metrics, spool: spool, spoolLogs: spoolLogs, spoolMetrics: spoolMetrics, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	b.wg.Add(1)
	go b.loop()
	b.signal()
	return b
}

func (b *spooledBackend) AppendLogs(ctx context.Context, lines []LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	if !b.spoolLogs {
		return b.logs.AppendLogs(ctx, lines)
	}
	if err := b.spool.assignLogs(lines); err != nil {
		return err
	}
	values := append([]LogLine(nil), lines...)
	if _, err := b.spool.put(spoolRecord{Kind: "logs", Logs: values}); err != nil {
		b.setError(err)
		return err
	}
	b.signal()
	return nil
}

func (b *spooledBackend) AppendMetrics(ctx context.Context, points []MetricPoint) error {
	if len(points) == 0 {
		return nil
	}
	if !b.spoolMetrics {
		return b.metrics.AppendMetrics(ctx, points)
	}
	if err := b.spool.assignMetrics(points); err != nil {
		return err
	}
	values := append([]MetricPoint(nil), points...)
	if _, err := b.spool.put(spoolRecord{Kind: "metrics", Metrics: values}); err != nil {
		b.setError(err)
		return err
	}
	b.signal()
	return nil
}

func (b *spooledBackend) QueryLogs(ctx context.Context, query LogQuery) (LogPage, error) {
	if !b.spoolLogs {
		return b.logs.QueryLogs(ctx, query)
	}
	// Keep the backend read and spool snapshot in the same delivery epoch. Without
	// this lock, flush can insert a record into the backend and acknowledge its
	// spool file between the two reads, making the record temporarily invisible.
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	page, err := b.logs.QueryLogs(ctx, withLogLimit(query, MaxPageLimit))
	backendErr := err
	if err != nil {
		if !errors.Is(err, ErrBackendUnavailable) {
			err = errors.Join(ErrBackendUnavailable, err)
		}
		b.setError(err)
		page = LogPage{}
	}
	records, spoolErr := b.spool.records("logs")
	if spoolErr != nil {
		return LogPage{}, spoolErr
	}
	byEvent := make(map[string]LogLine, len(page.Lines))
	for _, line := range page.Lines {
		byEvent[logDedupKey(line)] = line
	}
	for _, record := range records {
		for _, line := range record.record.Logs {
			if matchLog(line, query) {
				byEvent[logDedupKey(line)] = line
			}
		}
	}
	lines := make([]LogLine, 0, len(byEvent))
	for _, line := range byEvent {
		lines = append(lines, line)
	}
	if backendErr != nil && len(lines) == 0 {
		return LogPage{}, backendErr
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	return logPageFrom(lines, query), nil
}

func (b *spooledBackend) QueryMetrics(ctx context.Context, query MetricQuery) (MetricPage, error) {
	if !b.spoolMetrics {
		return b.metrics.QueryMetrics(ctx, query)
	}
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	page, err := b.metrics.QueryMetrics(ctx, withMetricLimit(query, MaxPageLimit))
	backendErr := err
	if err != nil {
		if !errors.Is(err, ErrBackendUnavailable) {
			err = errors.Join(ErrBackendUnavailable, err)
		}
		b.setError(err)
		page = MetricPage{}
	}
	records, spoolErr := b.spool.records("metrics")
	if spoolErr != nil {
		return MetricPage{}, spoolErr
	}
	byEvent := make(map[string]MetricPoint, len(page.Points))
	for _, point := range page.Points {
		byEvent[metricDedupKey(point)] = point
	}
	for _, record := range records {
		for _, point := range record.record.Metrics {
			if matchMetric(point, query) {
				byEvent[metricDedupKey(point)] = point
			}
		}
	}
	points := make([]MetricPoint, 0, len(byEvent))
	for _, point := range byEvent {
		points = append(points, point)
	}
	if backendErr != nil && len(points) == 0 {
		return MetricPage{}, backendErr
	}
	sort.Slice(points, func(i, j int) bool { return points[i].ID < points[j].ID })
	return metricPageFrom(points, query), nil
}

func (b *spooledBackend) loop() {
	defer b.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.wake:
		case <-ticker.C:
		}
		errGen := b.errorGeneration()
		var flushErrors []error
		if b.spoolLogs {
			flushErrors = append(flushErrors, b.flushKind("logs"))
		}
		if b.spoolMetrics {
			flushErrors = append(flushErrors, b.flushKind("metrics"))
		}
		if err := errors.Join(flushErrors...); err != nil {
			b.setError(err)
		} else {
			b.clearErrorIfGeneration(errGen)
		}
	}
}

func (b *spooledBackend) flushKind(kind string) error {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	records, err := b.spool.records(kind)
	if err != nil {
		return err
	}
	for _, record := range records {
		ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
		if kind == "logs" {
			err = b.logs.AppendLogs(ctx, record.record.Logs)
		} else {
			err = b.metrics.AppendMetrics(ctx, record.record.Metrics)
		}
		cancel()
		if err != nil {
			return errors.Join(ErrBackendUnavailable, err)
		}
		if err = b.spool.ack(record); err != nil {
			return err
		}
	}
	return nil
}

func (b *spooledBackend) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *spooledBackend) close() { b.cancel(); b.wg.Wait() }

func (b *spooledBackend) PurgeProject(ctx context.Context, projectID string) error {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	if purger, ok := b.logs.(Purger); ok {
		if err := purger.PurgeProject(ctx, projectID); err != nil {
			return err
		}
	}
	if purger, ok := b.metrics.(Purger); ok {
		if err := purger.PurgeProject(ctx, projectID); err != nil {
			return err
		}
	}
	return b.spool.purge(projectID, "")
}

func (b *spooledBackend) PurgeRun(ctx context.Context, projectID, runID string) error {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	if purger, ok := b.logs.(Purger); ok {
		if err := purger.PurgeRun(ctx, projectID, runID); err != nil {
			return err
		}
	}
	if purger, ok := b.metrics.(Purger); ok {
		if err := purger.PurgeRun(ctx, projectID, runID); err != nil {
			return err
		}
	}
	return b.spool.purge(projectID, runID)
}

func (b *spooledBackend) setError(err error) {
	b.mu.Lock()
	wasDegraded := b.lastErr != nil
	b.lastErr = err
	b.errGen++
	b.mu.Unlock()
	if err != nil && !wasDegraded {
		slog.Warn("statistics delivery degraded", "reason", publicStatsError(err))
	}
	if err == nil && wasDegraded {
		slog.Info("statistics delivery recovered")
	}
}

func (b *spooledBackend) errorGeneration() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.errGen
}

// clearErrorIfGeneration applies a successful flush only when no newer
// operation has reported an error since that flush began.
func (b *spooledBackend) clearErrorIfGeneration(generation uint64) {
	b.mu.Lock()
	if b.errGen != generation {
		b.mu.Unlock()
		return
	}
	wasDegraded := b.lastErr != nil
	b.lastErr = nil
	b.mu.Unlock()
	if wasDegraded {
		slog.Info("statistics delivery recovered")
	}
}

func (b *spooledBackend) health() Health {
	b.mu.RLock()
	err := b.lastErr
	b.mu.RUnlock()
	b.spool.mu.Lock()
	pending := b.spool.bytes
	b.spool.mu.Unlock()
	h := Health{Healthy: err == nil, Degraded: err != nil || pending > 0, PendingBytes: pending}
	if err != nil {
		h.LastError = publicStatsError(err)
	}
	return h
}

func publicStatsError(err error) string {
	if errors.Is(err, ErrSpoolFull) {
		return "statistics spool is full"
	}
	return "statistics backend unavailable"
}

func withLogLimit(q LogQuery, limit int) LogQuery          { q.Limit = limit; return q }
func withMetricLimit(q MetricQuery, limit int) MetricQuery { q.Limit = limit; return q }

func matchLog(line LogLine, q LogQuery) bool {
	after, err := LogIDFromCursor(q.Cursor, q)
	return err == nil && line.ID > after && line.ProjectID == q.ProjectID && line.RunID == q.RunID && line.StepName == q.StepName &&
		(q.Since.IsZero() || !line.Ts.Before(q.Since)) && (q.Until.IsZero() || !line.Ts.After(q.Until)) &&
		(q.Search == "" || strings.Contains(line.Line, q.Search))
}

func matchMetric(point MetricPoint, q MetricQuery) bool {
	after, err := MetricIDFromCursor(q.Cursor, q)
	if err != nil || point.ID <= after || point.ProjectID != q.ProjectID || point.RunID != q.RunID || point.StepName != q.StepName ||
		(!q.Since.IsZero() && point.Ts.Before(q.Since)) || (!q.Until.IsZero() && point.Ts.After(q.Until)) {
		return false
	}
	if len(q.Keys) == 0 {
		return true
	}
	for _, key := range q.Keys {
		if key == point.Key {
			return true
		}
	}
	return false
}

func logPageFrom(lines []LogLine, query LogQuery) LogPage {
	limit := NormalizeLimit(query.Limit)
	page := LogPage{Lines: lines}
	if len(lines) > limit {
		page.Lines = lines[:limit]
		page.NextCursor = CursorForLogQuery(page.Lines[limit-1].ID, query)
	}
	return page
}

func metricPageFrom(points []MetricPoint, query MetricQuery) MetricPage {
	limit := NormalizeLimit(query.Limit)
	page := MetricPage{Points: points}
	if len(points) > limit {
		page.Points = points[:limit]
		page.NextCursor = CursorForMetricQuery(page.Points[limit-1].ID, query)
	}
	return page
}

func logDedupKey(line LogLine) string {
	if line.EventID != "" {
		return "event:" + line.EventID
	}
	return "id:" + strconv.FormatInt(line.ID, 10)
}
func metricDedupKey(point MetricPoint) string {
	if point.EventID != "" {
		return "event:" + point.EventID
	}
	return "id:" + strconv.FormatInt(point.ID, 10)
}
