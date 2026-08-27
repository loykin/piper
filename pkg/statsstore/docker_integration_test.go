package statsstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func integrationURL(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not set", name)
	}
	return value
}

func TestDockerElasticsearchIntegration(t *testing.T) {
	raw := integrationURL(t, "PIPER_TEST_ELASTICSEARCH_URL")
	ctx := context.Background()
	backend, err := openElasticsearch(raw, nil, time.Hour, 2*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	project := "docker-es"
	defer func() { _ = backend.PurgeProject(ctx, project) }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	lines := []LogLine{{ID: 1, EventID: "es-log-1", ProjectID: project, RunID: "run", StepName: "step", Ts: now, Stream: "stdout", Line: "first searchable"}, {ID: 2, EventID: "es-log-2", ProjectID: project, RunID: "run", StepName: "step", Ts: now, Stream: "stderr", Line: "second"}}
	if err = backend.AppendLogs(ctx, lines); err != nil {
		t.Fatal(err)
	}
	if err = backend.AppendLogs(ctx, lines[:1]); err != nil {
		t.Fatal(err)
	}
	first, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Search: "searchable", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 1 || first.Lines[0].EventID != "es-log-1" {
		t.Fatalf("first=%+v", first)
	}
	all, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Lines) != 2 {
		t.Fatalf("dedup/order page=%+v", all)
	}
	pageOne, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 1})
	if err != nil || pageOne.NextCursor == "" {
		t.Fatalf("first cursor page=%+v err=%v", pageOne, err)
	}
	pageTwo, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Cursor: pageOne.NextCursor, Limit: 1})
	if err != nil || len(pageTwo.Lines) != 1 || pageTwo.Lines[0].ID != 2 {
		t.Fatalf("second cursor page=%+v err=%v", pageTwo, err)
	}
	points := []MetricPoint{{ID: 1, EventID: "es-metric-1", ProjectID: project, RunID: "run", StepName: "step", Key: "loss", Value: .5, Ts: now}}
	if err = backend.AppendMetrics(ctx, points); err != nil {
		t.Fatal(err)
	}
	metrics, err := backend.QueryMetrics(ctx, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Keys: []string{"loss"}, Limit: 10})
	if err != nil || len(metrics.Points) != 1 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	if err = backend.PurgeProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	eventuallyEmptyLogs(t, backend, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 10})
}

func TestDockerClickHouseIntegration(t *testing.T) {
	raw := integrationURL(t, "PIPER_TEST_CLICKHOUSE_URL")
	ctx := context.Background()
	credential := map[string]string{"username": os.Getenv("PIPER_TEST_CLICKHOUSE_USER"), "password": os.Getenv("PIPER_TEST_CLICKHOUSE_PASSWORD")}
	backend, err := openClickHouse(raw, credential, time.Hour, 2*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	project := "docker-ch"
	defer func() { _ = backend.PurgeProject(ctx, project) }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	lines := []LogLine{{ID: 1, EventID: "ch-log-1", ProjectID: project, RunID: "run", StepName: "step", Ts: now, Stream: "stdout", Line: "first searchable"}, {ID: 2, EventID: "ch-log-2", ProjectID: project, RunID: "run", StepName: "step", Ts: now, Stream: "stderr", Line: "second"}}
	if err = backend.AppendLogs(ctx, lines); err != nil {
		t.Fatal(err)
	}
	if err = backend.AppendLogs(ctx, lines[:1]); err != nil {
		t.Fatal(err)
	}
	page, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%+v", page)
	}
	next, err := backend.QueryLogs(ctx, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Cursor: page.NextCursor, Limit: 1})
	if err != nil || len(next.Lines) != 1 || next.Lines[0].ID != 2 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	points := []MetricPoint{{ID: 1, EventID: "ch-metric-1", ProjectID: project, RunID: "run", StepName: "step", Key: "loss", Value: .5, Ts: now}}
	if err = backend.AppendMetrics(ctx, points); err != nil {
		t.Fatal(err)
	}
	metrics, err := backend.QueryMetrics(ctx, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Keys: []string{"loss"}, Limit: 10})
	if err != nil || len(metrics.Points) != 1 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	if err = backend.PurgeProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	eventuallyEmptyLogs(t, backend, LogQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 10})
}

func TestDockerInfluxDBIntegration(t *testing.T) {
	raw := integrationURL(t, "PIPER_TEST_INFLUXDB_URL")
	token := integrationURL(t, "PIPER_TEST_INFLUXDB_TOKEN")
	org := integrationURL(t, "PIPER_TEST_INFLUXDB_ORG")
	orgID := lookupInfluxOrgID(t, raw, org, token)
	backend, err := openInfluxDB(raw, map[string]string{"token": token, "org_id": orgID}, 2*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	project := "docker-influx"
	defer func() { _ = backend.PurgeProject(ctx, project) }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	points := []MetricPoint{{ID: 1, EventID: "influx-1", ProjectID: project, RunID: "run", StepName: "step", Key: "loss", Value: .5, Ts: now}, {ID: 2, EventID: "influx-2", ProjectID: project, RunID: "run", StepName: "step", Key: "accuracy", Value: .9, Ts: now.Add(time.Millisecond)}}
	if err = backend.AppendMetrics(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err = backend.AppendMetrics(ctx, points[:1]); err != nil {
		t.Fatal(err)
	}
	page, err := backend.QueryMetrics(ctx, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Keys: []string{"loss"}, Limit: 10})
	if err != nil || len(page.Points) != 1 || page.Points[0].EventID != "influx-1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	first, err := backend.QueryMetrics(ctx, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first cursor page=%+v err=%v", first, err)
	}
	second, err := backend.QueryMetrics(ctx, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Cursor: first.NextCursor, Limit: 1})
	if err != nil || len(second.Points) != 1 || second.Points[0].ID != 2 {
		t.Fatalf("second cursor page=%+v err=%v", second, err)
	}
	if err = backend.PurgeProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	eventuallyEmptyMetrics(t, backend, MetricQuery{ProjectID: project, RunID: "run", StepName: "step", Limit: 10})
}

func lookupInfluxOrgID(t *testing.T, raw, org, token string) string {
	t.Helper()
	h, _, err := newHTTPBackend(raw, map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	h.base.Path = ""
	h.tokenScheme = "Token"
	data, err := h.request(context.Background(), http.MethodGet, "api/v2/orgs", url.Values{"org": {org}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Orgs []struct {
			ID string `json:"id"`
		} `json:"orgs"`
	}
	if err = json.Unmarshal(data, &response); err != nil || len(response.Orgs) != 1 {
		t.Fatalf("org lookup response=%s err=%v", data, err)
	}
	return response.Orgs[0].ID
}

func eventuallyEmptyLogs(t *testing.T, backend LogBackend, q LogQuery) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, err := backend.QueryLogs(context.Background(), q)
		if err == nil && len(page.Lines) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("purged logs remained visible")
}

func eventuallyEmptyMetrics(t *testing.T, backend MetricBackend, q MetricQuery) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, err := backend.QueryMetrics(context.Background(), q)
		if err == nil && len(page.Points) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("purged metrics remained visible")
}
