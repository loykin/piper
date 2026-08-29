package statsstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func backendURL(serverURL, scheme, suffix string) string {
	u, _ := url.Parse(serverURL)
	u.Scheme = scheme
	parts := strings.SplitN(suffix, "?", 2)
	u.Path = parts[0]
	if len(parts) == 2 {
		u.RawQuery = parts[1]
	}
	return u.String()
}

func TestElasticsearchAdapterBulkSearchAndCredential(t *testing.T) {
	var mu sync.Mutex
	var bulk string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization=%q", got)
		}
		switch {
		case r.URL.Path == "/_bulk":
			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			bulk = string(data)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"errors":false}`))
		case strings.HasSuffix(r.URL.Path, "/_search"):
			_, _ = w.Write([]byte(`{"hits":{"hits":[{"_source":{"id":7,"event_id":"e7","project_id":"p","run_id":"r","step_name":"s","ts":"2026-08-27T00:00:00Z","stream":"stdout","line":"hello"}}]}}`))
		case strings.HasPrefix(r.URL.Path, "/_index_template/"):
			// Field mappings are applied unconditionally on Open, regardless
			// of manage — see openElasticsearch.
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	b, err := openElasticsearch(backendURL(server.URL, "elasticsearch", "piper"), map[string]string{"token": "secret"}, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	line := LogLine{ID: 7, EventID: "e7", ProjectID: "p", RunID: "r", StepName: "s", Ts: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), Stream: "stdout", Line: "hello"}
	if err = b.AppendLogs(context.Background(), []LogLine{line}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotBulk := bulk
	mu.Unlock()
	if !strings.Contains(gotBulk, `"_id":"e7"`) || !strings.Contains(gotBulk, `"project_id":"p"`) {
		t.Fatalf("bulk=%s", gotBulk)
	}
	page, err := b.QueryLogs(context.Background(), LogQuery{ProjectID: "p", RunID: "r", StepName: "s", Limit: 10})
	if err != nil || len(page.Lines) != 1 || page.Lines[0].EventID != "e7" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestClickHouseAdapterUsesJSONEachRowAndBoundedQuery(t *testing.T) {
	var inserts, selects int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.HasPrefix(q, "INSERT") {
			inserts++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"event_id":"m1"`) {
				t.Errorf("insert=%s", body)
			}
		} else if strings.HasPrefix(q, "SELECT") {
			selects++
			if !strings.Contains(q, "LIMIT 3") || !strings.Contains(q, "project_id='p'") {
				t.Errorf("query=%s", q)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":1,"event_id":"m1","project_id":"p","run_id":"r","step_name":"s","key":"loss","value":0.5,"ts":"2026-08-27T00:00:00Z"}]}`))
		} else if strings.HasPrefix(q, "CREATE") {
			// Database/table creation always runs on Open, regardless of
			// manage — see openClickHouse.
		} else {
			t.Errorf("unexpected query=%s", q)
		}
	}))
	defer server.Close()
	b, err := openClickHouse(backendURL(server.URL, "clickhouse", "db?metrics_table=metrics"), nil, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	point := MetricPoint{ID: 1, EventID: "m1", ProjectID: "p", RunID: "r", StepName: "s", Key: "loss", Value: .5, Ts: time.Now()}
	if err = b.AppendMetrics(context.Background(), []MetricPoint{point}); err != nil {
		t.Fatal(err)
	}
	page, err := b.QueryMetrics(context.Background(), MetricQuery{ProjectID: "p", RunID: "r", StepName: "s", Limit: 2})
	if err != nil || len(page.Points) != 1 || inserts != 1 || selects != 1 {
		t.Fatalf("page=%+v inserts=%d selects=%d err=%v", page, inserts, selects, err)
	}
}

func TestInfluxAdapterWritesLineProtocolWithoutURLSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/write" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("org") != "acme" || r.URL.Query().Get("bucket") != "piper" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Token token-value" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "event_id=e1") {
			t.Errorf("body=%s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	b, err := openInfluxDB(backendURL(server.URL, "influxdb", "piper?org=acme"), map[string]string{"token": "token-value"}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	err = b.AppendMetrics(context.Background(), []MetricPoint{{ID: 1, EventID: "e1", ProjectID: "p", RunID: "r", StepName: "s", Key: "loss", Value: 1, Ts: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackendURLValidationRejectsLokiAndSecrets(t *testing.T) {
	for _, tc := range []struct{ kind, url string }{{"logs", "loki://host/x"}, {"logs", "elasticsearch://user:pass@host/x"}, {"metrics", "influxdb://host/b?org=o&token=secret"}, {"logs", "influxdb://host/b?org=o"}} {
		if err := ValidateBackendURL(tc.kind, tc.url); err == nil {
			t.Errorf("accepted %s", tc.url)
		}
	}
	if err := ValidateBackendURL("metrics", "influxdb+https://host/bucket?org=acme"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedRetentionConfiguresNativePolicies(t *testing.T) {
	t.Run("elasticsearch-ilm", func(t *testing.T) {
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		_, err := openElasticsearch(backendURL(server.URL, "elasticsearch", "piper"), nil, 24*time.Hour, 48*time.Hour, true)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(paths, " ")
		for _, want := range []string{"/_ilm/policy/piper-logs-retention", "/_ilm/policy/piper-metrics-retention", "/_index_template/piper-logs-template", "/_index_template/piper-metrics-template"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("paths=%v missing %s", paths, want)
			}
		}
	})
	t.Run("clickhouse-ttl", func(t *testing.T) {
		var queries []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { queries = append(queries, r.URL.Query().Get("query")) }))
		defer server.Close()
		_, err := openClickHouse(backendURL(server.URL, "clickhouse", "db"), nil, time.Hour, 2*time.Hour, true)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(queries, " ")
		if !strings.Contains(joined, "TTL toDateTime(ts) + INTERVAL 3600 SECOND") || !strings.Contains(joined, "TTL toDateTime(ts) + INTERVAL 7200 SECOND") {
			t.Fatalf("queries=%v", queries)
		}
	})
	t.Run("influx-bucket-retention", func(t *testing.T) {
		var patched string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"buckets":[{"id":"bucket-id"}]}`))
				return
			}
			if r.Method == http.MethodPatch {
				data, _ := io.ReadAll(r.Body)
				patched = string(data)
				return
			}
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}))
		defer server.Close()
		_, err := openInfluxDB(backendURL(server.URL, "influxdb", "piper?org=acme"), map[string]string{"token": "secret", "org_id": "org-id"}, 3*time.Hour, true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(patched, `"everySeconds":10800`) {
			t.Fatalf("patch=%s", patched)
		}
	})
}

// TestSchemaSetupIsUnconditionalOnManage guards against a real live-testing
// regression: with manage (ManageRetention) false, Elasticsearch never
// applied its index template, so dynamic mapping turned project_id/run_id/
// step_name into analyzed "text" fields — the standard analyzer then splits
// a hyphenated UUID run_id into multiple tokens, silently breaking every
// term/range filter QueryLogs/QueryMetrics relies on, even though writes
// kept succeeding. Symmetrically, ClickHouse never ran CREATE
// DATABASE/TABLE, so AppendLogs/AppendMetrics failed outright with "Database
// ... does not exist". Both must set up schema/mappings unconditionally and
// gate only the retention policy (ILM policy / TTL clause) on manage.
func TestSchemaSetupIsUnconditionalOnManage(t *testing.T) {
	t.Run("elasticsearch-mapping-without-manage", func(t *testing.T) {
		var sawTemplate bool
		var templateBody string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/_ilm/policy/") {
				t.Errorf("ILM policy must not be created when manage=false, got %s", r.URL.Path)
			}
			if strings.HasPrefix(r.URL.Path, "/_index_template/") {
				sawTemplate = true
				data, _ := io.ReadAll(r.Body)
				templateBody = string(data)
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		_, err := openElasticsearch(backendURL(server.URL, "elasticsearch", "piper"), nil, 0, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if !sawTemplate {
			t.Fatal("index template was not applied even though manage=false")
		}
		if !strings.Contains(templateBody, `"project_id":{"type":"keyword"}`) || !strings.Contains(templateBody, `"run_id":{"type":"keyword"}`) {
			t.Fatalf("template body missing keyword mappings: %s", templateBody)
		}
	})
	t.Run("clickhouse-tables-without-manage", func(t *testing.T) {
		var sawCreateTable, sawTTL bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("query")
			if strings.HasPrefix(q, "CREATE TABLE") {
				sawCreateTable = true
			}
			if strings.Contains(q, "TTL") {
				sawTTL = true
			}
		}))
		defer server.Close()
		_, err := openClickHouse(backendURL(server.URL, "clickhouse", "db"), nil, time.Hour, time.Hour, false)
		if err != nil {
			t.Fatal(err)
		}
		if !sawCreateTable {
			t.Fatal("tables were not created even though manage=false")
		}
		if sawTTL {
			t.Fatal("TTL clause must not be applied when manage=false")
		}
	})
}
