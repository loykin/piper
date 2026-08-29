package statsstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type elasticsearchBackend struct {
	http                    *httpBackend
	logsIndex, metricsIndex string
}

func openElasticsearch(rawURL string, credential map[string]string, logRetention, metricRetention time.Duration, manage bool) (*elasticsearchBackend, error) {
	h, parsed, err := newHTTPBackend(rawURL, credential)
	if err != nil {
		return nil, err
	}
	base := strings.Trim(parsed.Path, "/")
	if base == "" {
		base = "piper"
	}
	h.base.Path = ""
	b := &elasticsearchBackend{http: h, logsIndex: base + "-logs", metricsIndex: base + "-metrics"}
	// The index template (field mappings) is applied unconditionally, not
	// just when manage (ManageRetention) is set. Without it, Elasticsearch's
	// dynamic mapping turns project_id/run_id/step_name/stream into analyzed
	// "text" fields instead of "keyword" — the standard analyzer then splits
	// a hyphenated run_id (a UUID) into multiple tokens, so every `term`/
	// `range` filter QueryLogs/QueryMetrics relies on silently stops
	// matching, even though AppendLogs/AppendMetrics keep succeeding. Only
	// the ILM (retention) policy itself stays conditional on manage.
	for i, index := range []string{b.logsIndex, b.metricsIndex} {
		retention := []time.Duration{logRetention, metricRetention}[i]
		properties := map[string]any{
			"id": map[string]string{"type": "long"}, "event_id": map[string]string{"type": "keyword"},
			"project_id": map[string]string{"type": "keyword"}, "run_id": map[string]string{"type": "keyword"},
			"step_name": map[string]string{"type": "keyword"}, "ts": map[string]string{"type": "date"},
		}
		if i == 0 {
			properties["stream"] = map[string]string{"type": "keyword"}
			properties["line"] = map[string]string{"type": "text"}
		} else {
			properties["key"] = map[string]string{"type": "keyword"}
			properties["value"] = map[string]string{"type": "double"}
		}
		mapping := map[string]any{"mappings": map[string]any{"properties": properties}}
		if manage && retention > 0 {
			policy := index + "-retention"
			policyBody, _ := jsonBody(map[string]any{"policy": map[string]any{"phases": map[string]any{"delete": map[string]any{"min_age": fmt.Sprintf("%ds", int64(retention.Seconds())), "actions": map[string]any{"delete": map[string]any{}}}}}})
			if _, err := h.request(context.Background(), http.MethodPut, "_ilm/policy/"+policy, nil, policyBody, "application/json"); err != nil {
				return nil, err
			}
			mapping["settings"] = map[string]any{"index.lifecycle.name": policy}
		}
		templateBody, _ := jsonBody(map[string]any{"index_patterns": []string{index + "*"}, "template": mapping})
		if _, err := h.request(context.Background(), http.MethodPut, "_index_template/"+index+"-template", nil, templateBody, "application/json"); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *elasticsearchBackend) append(ctx context.Context, index string, values []any) error {
	var body bytes.Buffer
	for _, value := range values {
		var eventID string
		switch v := value.(type) {
		case LogLine:
			eventID = v.EventID
		case MetricPoint:
			eventID = v.EventID
		}
		meta, _ := json.Marshal(map[string]any{"index": map[string]string{"_index": index, "_id": eventID}})
		row, _ := json.Marshal(value)
		body.Write(meta)
		body.WriteByte('\n')
		body.Write(row)
		body.WriteByte('\n')
	}
	data, err := b.http.request(ctx, http.MethodPost, "_bulk", url.Values{"refresh": {"wait_for"}}, body.Bytes(), "application/x-ndjson")
	if err != nil {
		return err
	}
	var response struct {
		Errors bool `json:"errors"`
	}
	if json.Unmarshal(data, &response) == nil && response.Errors {
		return fmt.Errorf("%w: elasticsearch bulk response contains failed items", ErrBackendUnavailable)
	}
	return nil
}

func (b *elasticsearchBackend) AppendLogs(ctx context.Context, lines []LogLine) error {
	values := make([]any, len(lines))
	for i := range lines {
		values[i] = lines[i]
	}
	return b.append(ctx, b.logsIndex, values)
}
func (b *elasticsearchBackend) AppendMetrics(ctx context.Context, points []MetricPoint) error {
	values := make([]any, len(points))
	for i := range points {
		values[i] = points[i]
	}
	return b.append(ctx, b.metricsIndex, values)
}

func elasticQuery(projectID, runID, stepName string, after int64, since, until any, limit int, extra []any) map[string]any {
	filters := []any{map[string]any{"term": map[string]string{"project_id": projectID}}, map[string]any{"term": map[string]string{"run_id": runID}}, map[string]any{"term": map[string]string{"step_name": stepName}}, map[string]any{"range": map[string]any{"id": map[string]any{"gt": after}}}}
	if t, ok := since.(interface {
		IsZero() bool
		Format(string) string
	}); ok && !t.IsZero() {
		filters = append(filters, map[string]any{"range": map[string]any{"ts": map[string]string{"gte": t.Format("2006-01-02T15:04:05.999999999Z07:00")}}})
	}
	if t, ok := until.(interface {
		IsZero() bool
		Format(string) string
	}); ok && !t.IsZero() {
		filters = append(filters, map[string]any{"range": map[string]any{"ts": map[string]string{"lte": t.Format("2006-01-02T15:04:05.999999999Z07:00")}}})
	}
	filters = append(filters, extra...)
	return map[string]any{"size": NormalizeLimit(limit) + 1, "sort": []any{map[string]string{"id": "asc"}}, "query": map[string]any{"bool": map[string]any{"filter": filters}}}
}

func (b *elasticsearchBackend) QueryLogs(ctx context.Context, q LogQuery) (LogPage, error) {
	extra := []any{}
	if q.Search != "" {
		extra = append(extra, map[string]any{"match": map[string]string{"line": q.Search}})
	}
	after, err := LogIDFromCursor(q.Cursor, q)
	if err != nil {
		return LogPage{}, err
	}
	query := elasticQuery(q.ProjectID, q.RunID, q.StepName, after, q.Since, q.Until, q.Limit, extra)
	if err != nil {
		return LogPage{}, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source LogLine `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err = b.search(ctx, b.logsIndex, query, &response); err != nil {
		return LogPage{}, err
	}
	lines := make([]LogLine, len(response.Hits.Hits))
	for i := range lines {
		lines[i] = response.Hits.Hits[i].Source
	}
	return logPageFrom(lines, q), nil
}
func (b *elasticsearchBackend) QueryMetrics(ctx context.Context, q MetricQuery) (MetricPage, error) {
	extra := []any{}
	if len(q.Keys) > 0 {
		extra = append(extra, map[string]any{"terms": map[string][]string{"key": q.Keys}})
	}
	after, err := MetricIDFromCursor(q.Cursor, q)
	if err != nil {
		return MetricPage{}, err
	}
	query := elasticQuery(q.ProjectID, q.RunID, q.StepName, after, q.Since, q.Until, q.Limit, extra)
	if err != nil {
		return MetricPage{}, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source MetricPoint `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err = b.search(ctx, b.metricsIndex, query, &response); err != nil {
		return MetricPage{}, err
	}
	points := make([]MetricPoint, len(response.Hits.Hits))
	for i := range points {
		points[i] = response.Hits.Hits[i].Source
	}
	return metricPageFrom(points, q), nil
}
func (b *elasticsearchBackend) search(ctx context.Context, index string, query any, out any) error {
	body, _ := jsonBody(query)
	data, err := b.http.request(ctx, http.MethodPost, index+"/_search", nil, body, "application/json")
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func (b *elasticsearchBackend) PurgeProject(ctx context.Context, projectID string) error {
	return b.deleteByQuery(ctx, map[string]any{"term": map[string]string{"project_id": projectID}})
}
func (b *elasticsearchBackend) PurgeRun(ctx context.Context, projectID, runID string) error {
	return b.deleteByQuery(ctx, map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]string{"project_id": projectID}}, map[string]any{"term": map[string]string{"run_id": runID}}}}})
}
func (b *elasticsearchBackend) deleteByQuery(ctx context.Context, q any) error {
	body, _ := jsonBody(map[string]any{"query": q})
	for _, index := range []string{b.logsIndex, b.metricsIndex} {
		if _, err := b.http.request(ctx, http.MethodPost, index+"/_delete_by_query", url.Values{"conflicts": {"proceed"}}, body, "application/json"); err != nil {
			return err
		}
	}
	return nil
}

var _ = strconv.Itoa
