package statsstore

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type influxBackend struct {
	http        *httpBackend
	org, bucket string
	retention   time.Duration
}

func openInfluxDB(rawURL string, credential map[string]string, retention time.Duration, manage bool) (*influxBackend, error) {
	h, parsed, err := newHTTPBackend(rawURL, credential)
	if err != nil {
		return nil, err
	}
	h.tokenScheme = "Token"
	bucket := strings.Trim(parsed.Path, "/")
	q := parsed.Query()
	if bucket == "" {
		bucket = q.Get("bucket")
	}
	org := q.Get("org")
	if org == "" {
		org = credential["org"]
	}
	if bucket == "" || org == "" {
		return nil, fmt.Errorf("InfluxDB stats URL requires bucket path and org query (or credential org)")
	}
	h.base.Path = ""
	b := &influxBackend{http: h, org: org, bucket: bucket, retention: retention}
	if manage {
		orgID := credential["org_id"]
		if orgID == "" {
			return nil, fmt.Errorf("InfluxDB manage_retention requires org_id in credential_ref")
		}
		rules := []map[string]any{}
		if retention > 0 {
			seconds := int64(retention.Seconds())
			shardSeconds := seconds / 2
			if shardSeconds < 1 {
				shardSeconds = 1
			}
			rules = append(rules, map[string]any{"type": "expire", "everySeconds": seconds, "shardGroupDurationSeconds": shardSeconds})
		}
		data, listErr := h.request(context.Background(), http.MethodGet, "api/v2/buckets", url.Values{"orgID": {orgID}, "name": {bucket}}, nil, "")
		if listErr != nil {
			return nil, listErr
		}
		var found struct {
			Buckets []struct {
				ID string `json:"id"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(data, &found); err != nil {
			return nil, err
		}
		if len(found.Buckets) == 0 {
			payload, _ := jsonBody(map[string]any{"orgID": orgID, "name": bucket, "retentionRules": rules})
			if _, err = h.request(context.Background(), http.MethodPost, "api/v2/buckets", nil, payload, "application/json"); err != nil {
				return nil, err
			}
		} else {
			payload, _ := jsonBody(map[string]any{"retentionRules": rules})
			if _, err = h.request(context.Background(), http.MethodPatch, "api/v2/buckets/"+found.Buckets[0].ID, nil, payload, "application/json"); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}
func influxEscape(v string) string {
	r := strings.NewReplacer(" ", "\\ ", ",", "\\,", "=", "\\=")
	return r.Replace(v)
}
func (b *influxBackend) AppendMetrics(ctx context.Context, points []MetricPoint) error {
	var body bytes.Buffer
	for _, p := range points {
		fmt.Fprintf(&body, "run_metrics,project_id=%s,run_id=%s,step_name=%s,key=%s,event_id=%s value=%s,id=%di %d\n", influxEscape(p.ProjectID), influxEscape(p.RunID), influxEscape(p.StepName), influxEscape(p.Key), influxEscape(p.EventID), strconv.FormatFloat(p.Value, 'g', -1, 64), p.ID, p.Ts.UnixNano())
	}
	_, err := b.http.request(ctx, http.MethodPost, "api/v2/write", url.Values{"org": {b.org}, "bucket": {b.bucket}, "precision": {"ns"}}, body.Bytes(), "text/plain")
	return err
}
func fluxQuote(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}
func (b *influxBackend) QueryMetrics(ctx context.Context, q MetricQuery) (MetricPage, error) {
	after, err := MetricIDFromCursor(q.Cursor, q)
	if err != nil {
		return MetricPage{}, err
	}
	start := "0"
	if !q.Since.IsZero() {
		start = q.Since.UTC().Format(time.RFC3339Nano)
	}
	stop := "now()"
	if !q.Until.IsZero() {
		stop = q.Until.UTC().Add(time.Nanosecond).Format(time.RFC3339Nano)
	}
	filter := fmt.Sprintf(`r._measurement == "run_metrics" and r.project_id == %s and r.run_id == %s and r.step_name == %s`, fluxQuote(q.ProjectID), fluxQuote(q.RunID), fluxQuote(q.StepName))
	if len(q.Keys) > 0 {
		parts := make([]string, len(q.Keys))
		for i, k := range q.Keys {
			parts[i] = "r.key == " + fluxQuote(k)
		}
		filter += " and (" + strings.Join(parts, " or ") + ")"
	}
	flux := fmt.Sprintf(`from(bucket:%s) |> range(start:%s, stop:%s) |> filter(fn:(r) => %s) |> pivot(rowKey:["_time","event_id"], columnKey:["_field"], valueColumn:"_value") |> filter(fn:(r) => r.id > %d) |> sort(columns:["id"]) |> limit(n:%d)`, fluxQuote(b.bucket), start, stop, filter, after, NormalizeLimit(q.Limit)+1)
	payload, _ := jsonBody(map[string]string{"query": flux, "type": "flux"})
	data, err := b.http.request(ctx, http.MethodPost, "api/v2/query", url.Values{"org": {b.org}}, payload, "application/json")
	if err != nil {
		return MetricPage{}, err
	}
	points, err := parseInfluxMetrics(data)
	if err != nil {
		return MetricPage{}, err
	}
	return metricPageFrom(points, q), nil
}
func parseInfluxMetrics(data []byte) ([]MetricPoint, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	var header []string
	var result []MetricPoint
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(row) == 0 || strings.HasPrefix(row[0], "#") {
			continue
		}
		if header == nil || csvHeaderRow(row) {
			header = row
			continue
		}
		values := map[string]string{}
		for i, key := range header {
			if i < len(row) {
				values[key] = row[i]
			}
		}
		id, _ := strconv.ParseInt(values["id"], 10, 64)
		value, _ := strconv.ParseFloat(values["value"], 64)
		ts, _ := time.Parse(time.RFC3339Nano, values["_time"])
		result = append(result, MetricPoint{ID: id, EventID: values["event_id"], ProjectID: values["project_id"], RunID: values["run_id"], StepName: values["step_name"], Key: values["key"], Value: value, Ts: ts})
	}
	return result, nil
}

func csvHeaderRow(row []string) bool {
	hasTime, hasID := false, false
	for _, value := range row {
		hasTime = hasTime || value == "_time"
		hasID = hasID || value == "id"
	}
	return hasTime && hasID
}
func (b *influxBackend) PurgeProject(ctx context.Context, projectID string) error {
	return b.purge(ctx, fmt.Sprintf(`_measurement="run_metrics" AND project_id=%s`, fluxQuote(projectID)))
}
func (b *influxBackend) PurgeRun(ctx context.Context, projectID, runID string) error {
	return b.purge(ctx, fmt.Sprintf(`_measurement="run_metrics" AND project_id=%s AND run_id=%s`, fluxQuote(projectID), fluxQuote(runID)))
}
func (b *influxBackend) purge(ctx context.Context, predicate string) error {
	start := "1970-01-01T00:00:00Z"
	payload, _ := jsonBody(map[string]string{"start": start, "stop": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), "predicate": predicate})
	_, err := b.http.request(ctx, http.MethodPost, "api/v2/delete", url.Values{"org": {b.org}, "bucket": {b.bucket}}, payload, "application/json")
	return err
}
