package logstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/statsstore"
)

type captureStatsBackend struct {
	logs    []statsstore.LogLine
	metrics []statsstore.MetricPoint
}

func (b *captureStatsBackend) AppendLogs(_ context.Context, v []statsstore.LogLine) error {
	b.logs = append(b.logs, v...)
	return nil
}
func (b *captureStatsBackend) QueryLogs(context.Context, statsstore.LogQuery) (statsstore.LogPage, error) {
	return statsstore.LogPage{}, nil
}
func (b *captureStatsBackend) AppendMetrics(_ context.Context, v []statsstore.MetricPoint) error {
	b.metrics = append(b.metrics, v...)
	return nil
}
func (b *captureStatsBackend) QueryMetrics(context.Context, statsstore.MetricQuery) (statsstore.MetricPage, error) {
	return statsstore.MetricPage{}, nil
}

func TestStatsAdapterRedactsBeforeBackend(t *testing.T) {
	backend := &captureStatsBackend{}
	store := statsstore.NewStore(backend, backend, statsstore.Capabilities{}, nil)
	adapter := NewStatsAdapter(store)
	secret := "ghp_abcdefghijklmnopqrstuvwxyz123456"
	if err := adapter.Append(context.Background(), []*Line{{ProjectID: "p", RunID: "r", StepName: "s", Ts: time.Now(), Line: "token=" + secret}}); err != nil {
		t.Fatal(err)
	}
	if len(backend.logs) != 1 || strings.Contains(backend.logs[0].Line, secret) {
		t.Fatalf("unredacted logs=%+v", backend.logs)
	}
}
