package mlflow

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds how much of a remote MLflow response body this
// client will ever read into memory (design doc section 15.2: "응답 body
// 최대 크기 제한"). 1 MiB comfortably covers a SearchRuns page (bounded by
// MaxResults) or a LogBatch/UpdateRun ack, without letting a misbehaving or
// malicious tracking server exhaust Piper server memory.
const maxResponseBytes = 1 << 20

// HTTPClientConfig configures NewHTTPClient. Credential material (Token, or
// Username+Password) is read once at construction and never logged — see
// pkg/credential.Store.ResolveMlflow, the intended source of these fields.
type HTTPClientConfig struct {
	TrackingURI string
	Token       string // Bearer token; mutually exclusive with Username/Password
	Username    string
	Password    string
	// CACertPEM, when set, is trusted in addition to the system root pool
	// (design doc section 5.2's optional custom CA field).
	CACertPEM string
	Policy    SSRFPolicy
	// Timeout bounds each individual HTTP call. Defaults to 10s (design doc
	// section 13's `request_timeout` default) if zero.
	Timeout time.Duration
}

// httpClient is the real MLflow Tracking REST API implementation of
// Client (design doc section 12). It intentionally implements only the
// subset of the official REST API
// (https://mlflow.org/docs/latest/api_reference/rest-api.html) the
// exporter needs — no MLflow SDK subprocess, no Python sidecar, a plain Go
// net/http client against a single tracking server.
type httpClient struct {
	base       *url.URL
	httpClient *http.Client
	token      string
	username   string
	password   string
}

// NewHTTPClient builds a Client against cfg.TrackingURI. TrackingURI is
// re-validated against cfg.Policy here (defense in depth — the
// MLflowIntegration repository already validates it at write time, but a
// client can in principle be constructed from any URI a caller supplies).
func NewHTTPClient(cfg HTTPClientConfig) (Client, error) {
	if err := ValidateTrackingURI(cfg.TrackingURI, cfg.Policy); err != nil {
		return nil, err
	}
	if cfg.Token != "" && (cfg.Username != "" || cfg.Password != "") {
		return nil, fmt.Errorf("mlflow client: token is mutually exclusive with username/password")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.TrackingURI), "/"))
	if err != nil {
		return nil, fmt.Errorf("mlflow client: invalid tracking uri: %w", err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport, err := newSafeTransport(cfg.Policy, cfg.CACertPEM)
	if err != nil {
		return nil, err
	}
	return &httpClient{
		base: base,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			// Deny redirects by default (design doc section 5.3: "redirect는
			// 기본 거부하거나 같은 origin의 제한된 redirect만 허용한다") — a
			// redirect to an attacker-controlled host would bypass every
			// check above since Go's http.Client otherwise follows it
			// automatically (re-sending our Authorization header).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		token:    cfg.Token,
		username: cfg.Username,
		password: cfg.Password,
	}, nil
}

// newSafeTransport builds an http.Transport that resolves DNS itself and
// rejects a connection to any address that fails the policy's
// private/loopback/link-local + allowlist checks (SSRFPolicy, ssrf.go) —
// this is what closes the DNS-rebinding gap a plain scheme/host check on
// the URL alone would leave open (the URL's hostname can resolve to a safe
// address at validation time and a private one at dial time). Mirrors
// pkg/notify/http.go's safeHTTPClient, the precedent this package's own
// ssrf.go doc comments point at; kept as a separate implementation since
// notify's safeHTTPClient is unexported and this package must also honor
// SSRFPolicy's AllowedHosts/AllowedCIDRs, which notify's equivalent doesn't
// have.
func newSafeTransport(policy SSRFPolicy, caCertPEM string) (*http.Transport, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(caCertPEM) != "" {
		pool, err := loadCACertPool(caCertPEM)
		if err != nil {
			return nil, fmt.Errorf("mlflow client: invalid ca_cert: %w", err)
		}
		tlsConfig.RootCAs = pool
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		// A literal IP in the URL is still checked here (LookupIPAddr
		// accepts and returns it unchanged), so this single path covers
		// both the hostname and literal-IP cases.
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("mlflow tracking host did not resolve")
		}
		for _, resolved := range ips {
			if !publicIP(resolved.IP) {
				return nil, fmt.Errorf("mlflow tracking host resolved to a private or local address")
			}
			if len(policy.AllowedCIDRs) > 0 && !ipInAnyCIDR(resolved.IP, policy.AllowedCIDRs) {
				return nil, fmt.Errorf("mlflow tracking host resolved to an address outside the allowed CIDR ranges")
			}
		}
		if len(policy.AllowedHosts) > 0 && !hostAllowed(strings.ToLower(strings.TrimSuffix(host, ".")), policy.AllowedHosts) {
			return nil, fmt.Errorf("mlflow tracking host %q is not in the allowed host list", host)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return transport, nil
}

func loadCACertPool(pemData string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(pemData)) {
		return nil, fmt.Errorf("no valid PEM certificates found")
	}
	return pool, nil
}

// --- wire DTOs (MLflow Tracking REST API JSON shapes) ---

type wireTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type wireExperiment struct {
	ExperimentID     string    `json:"experiment_id"`
	Name             string    `json:"name"`
	ArtifactLocation string    `json:"artifact_location"`
	LifecycleStage   string    `json:"lifecycle_stage"`
	Tags             []wireTag `json:"tags"`
}

type wireRunInfo struct {
	RunID          string `json:"run_id"`
	ExperimentID   string `json:"experiment_id"`
	Status         string `json:"status"`
	StartTime      int64  `json:"start_time"`
	EndTime        int64  `json:"end_time"`
	ArtifactURI    string `json:"artifact_uri"`
	LifecycleStage string `json:"lifecycle_stage"`
}

type wireParam struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type wireMetric struct {
	Key       string  `json:"key"`
	Value     float64 `json:"value"`
	Timestamp int64   `json:"timestamp"`
	Step      int64   `json:"step"`
}

type wireRunData struct {
	Metrics []wireMetric `json:"metrics"`
	Params  []wireParam  `json:"params"`
	Tags    []wireTag    `json:"tags"`
}

type wireRun struct {
	Info wireRunInfo `json:"info"`
	Data wireRunData `json:"data"`
}

func (r wireRun) toRun() *Run {
	params := make(map[string]string, len(r.Data.Params))
	for _, p := range r.Data.Params {
		params[p.Key] = p.Value
	}
	tags := make(map[string]string, len(r.Data.Tags))
	for _, t := range r.Data.Tags {
		tags[t.Key] = t.Value
	}
	return &Run{
		RunID:          r.Info.RunID,
		ExperimentID:   r.Info.ExperimentID,
		Status:         RunStatus(r.Info.Status),
		StartTime:      r.Info.StartTime,
		EndTime:        r.Info.EndTime,
		ArtifactURI:    r.Info.ArtifactURI,
		LifecycleStage: r.Info.LifecycleStage,
		Params:         params,
		Tags:           tags,
	}
}

func (e wireExperiment) toExperiment() *Experiment {
	tags := make(map[string]string, len(e.Tags))
	for _, t := range e.Tags {
		tags[t.Key] = t.Value
	}
	return &Experiment{
		ExperimentID:     e.ExperimentID,
		Name:             e.Name,
		ArtifactLocation: e.ArtifactLocation,
		LifecycleStage:   e.LifecycleStage,
		Tags:             tags,
	}
}

func toWireTags(tags map[string]string) []wireTag {
	out := make([]wireTag, 0, len(tags))
	for k, v := range tags {
		out = append(out, wireTag{Key: k, Value: v})
	}
	return out
}

// --- Client methods ---

func (c *httpClient) GetExperimentByName(ctx context.Context, name string) (*Experiment, error) {
	var resp struct {
		Experiment wireExperiment `json:"experiment"`
	}
	err := c.do(ctx, http.MethodGet, "/api/2.0/mlflow/experiments/get-by-name", map[string]string{"experiment_name": name}, nil, &resp)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resp.Experiment.toExperiment(), nil
}

func (c *httpClient) CreateExperiment(ctx context.Context, req CreateExperimentRequest) (*Experiment, error) {
	body := map[string]any{
		"name": req.Name,
	}
	if req.ArtifactLocation != "" {
		body["artifact_location"] = req.ArtifactLocation
	}
	if len(req.Tags) > 0 {
		body["tags"] = toWireTags(req.Tags)
	}
	var resp struct {
		ExperimentID string `json:"experiment_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/2.0/mlflow/experiments/create", nil, body, &resp); err != nil {
		return nil, err
	}
	return &Experiment{ExperimentID: resp.ExperimentID, Name: req.Name, ArtifactLocation: req.ArtifactLocation, Tags: req.Tags}, nil
}

func (c *httpClient) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	tags := req.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	// mlflow.runName is the tag older MLflow servers derive the displayed
	// run name from; newer servers also accept a top-level run_name field.
	// Setting both keeps this client compatible across server versions —
	// see design doc section 21 item 4 (minimum supported MLflow
	// version/compatibility range is left an open decision; this is the
	// conservative choice that works either way).
	if req.RunName != "" {
		tags["mlflow.runName"] = req.RunName
	}
	body := map[string]any{
		"experiment_id": req.ExperimentID,
		"start_time":    req.StartTime,
		"tags":          toWireTags(tags),
	}
	if req.RunName != "" {
		body["run_name"] = req.RunName
	}
	var resp struct {
		Run wireRun `json:"run"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/2.0/mlflow/runs/create", nil, body, &resp); err != nil {
		return nil, err
	}
	return resp.Run.toRun(), nil
}

func (c *httpClient) GetRun(ctx context.Context, runID string) (*Run, error) {
	var resp struct {
		Run wireRun `json:"run"`
	}
	err := c.do(ctx, http.MethodGet, "/api/2.0/mlflow/runs/get", map[string]string{"run_id": runID}, nil, &resp)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resp.Run.toRun(), nil
}

func (c *httpClient) SearchRuns(ctx context.Context, req SearchRunsRequest) (RunPage, error) {
	body := map[string]any{
		"experiment_ids": req.ExperimentIDs,
	}
	if req.Filter != "" {
		body["filter"] = req.Filter
	}
	if req.MaxResults > 0 {
		body["max_results"] = req.MaxResults
	}
	if req.PageToken != "" {
		body["page_token"] = req.PageToken
	}
	var resp struct {
		Runs          []wireRun `json:"runs"`
		NextPageToken string    `json:"next_page_token"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/2.0/mlflow/runs/search", nil, body, &resp); err != nil {
		return RunPage{}, err
	}
	page := RunPage{NextPageToken: resp.NextPageToken}
	for _, r := range resp.Runs {
		page.Runs = append(page.Runs, r.toRun())
	}
	return page, nil
}

func (c *httpClient) LogBatch(ctx context.Context, req LogBatchRequest) error {
	metrics := make([]wireMetric, 0, len(req.Metrics))
	for _, m := range req.Metrics {
		metrics = append(metrics, wireMetric{Key: m.Key, Value: m.Value, Timestamp: m.Timestamp, Step: m.Step})
	}
	params := make([]wireParam, 0, len(req.Params))
	for _, p := range req.Params {
		params = append(params, wireParam{Key: p.Key, Value: p.Value})
	}
	tags := make([]wireTag, 0, len(req.Tags))
	for _, t := range req.Tags {
		tags = append(tags, wireTag{Key: t.Key, Value: t.Value})
	}
	body := map[string]any{
		"run_id":  req.RunID,
		"metrics": metrics,
		"params":  params,
		"tags":    tags,
	}
	return c.do(ctx, http.MethodPost, "/api/2.0/mlflow/runs/log-batch", nil, body, nil)
}

func (c *httpClient) UpdateRun(ctx context.Context, req UpdateRunRequest) error {
	body := map[string]any{
		"run_id": req.RunID,
	}
	if req.Status != "" {
		body["status"] = string(req.Status)
	}
	if req.EndTime > 0 {
		body["end_time"] = req.EndTime
	}
	if req.RunName != "" {
		body["run_name"] = req.RunName
	}
	return c.do(ctx, http.MethodPost, "/api/2.0/mlflow/runs/update", nil, body, nil)
}

// UploadArtifact is a best-effort implementation of MLflow's artifact-proxy
// upload API (PUT /api/2.0/mlflow-artifacts/artifacts/<path>), used by
// MLflow tracking servers configured with the proxied artifact store. It is
// not exercised by this phase's exporter — artifact manifest export is
// design doc section 8 / Phase 2, explicitly out of scope here — so this
// implementation has not been validated against a real MLflow server and
// should be treated as a starting point, not a verified integration, until
// Phase 2 wires it up and adds an httptest/integration test for it.
// runID resolves the run's artifact root via GetRun so relPath can stay a
// short, run-relative path (matching the shape design doc section 8.1's
// manifest.go sketch implies: a fixed "piper/artifacts.json").
func (c *httpClient) UploadArtifact(ctx context.Context, runID, relPath string, r io.Reader, size int64) error {
	run, err := c.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("mlflow client: run %q not found", redactID(runID))
	}
	artifactPath := strings.TrimPrefix(strings.TrimSuffix(run.ArtifactURI, "/")+"/"+strings.TrimPrefix(relPath, "/"), "/")
	u := *c.base
	u.Path = c.base.Path + "/api/2.0/mlflow-artifacts/artifacts/" + artifactPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), r)
	if err != nil {
		return err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newClientError(resp, nil)
	}
	return nil
}

// --- transport plumbing ---

func (c *httpClient) setAuth(req *http.Request) {
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case c.username != "" || c.password != "":
		req.SetBasicAuth(c.username, c.password)
	}
}

// do performs one MLflow REST call. method GET sends query as URL query
// params; other methods send reqBody as a JSON body. respOut, if non-nil,
// receives the JSON-decoded response body. Errors are always a
// *ClientError (or a wrapped network error classified via
// classifyNetworkError) with a redacted message — see newClientError.
func (c *httpClient) do(ctx context.Context, method, path string, query map[string]string, reqBody any, respOut any) error {
	u := *c.base
	u.Path = c.base.Path + path
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("mlflow client: encode request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("mlflow client: build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	raw, readErr := io.ReadAll(limited)
	if readErr != nil {
		return &ClientError{StatusCode: resp.StatusCode, Message: "failed reading mlflow response", retryable: true}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newClientError(resp, raw)
	}
	if respOut != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, respOut); err != nil {
			// Never echo the raw body into the error (design doc section
			// 15.2: "JSON decode 오류 시 앞부분을 그대로 log하지 않음").
			return &ClientError{StatusCode: resp.StatusCode, Message: "failed decoding mlflow response", retryable: false}
		}
	}
	return nil
}

func isNotFound(err error) bool {
	var ce *ClientError
	if !errors.As(err, &ce) {
		return false
	}
	return ce.StatusCode == http.StatusNotFound || ce.Code == "RESOURCE_DOES_NOT_EXIST"
}

// mlflowErrorBody mirrors MLflow's standard JSON error envelope:
// {"error_code": "...", "message": "..."}.
type mlflowErrorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// newClientError builds a *ClientError from an HTTP response, redacting
// the body down to MLflow's own error_code/message fields — never the raw
// bytes, which could contain an HTML proxy error page, a stack trace, or
// (in a misconfigured deployment) a reflected credential (design doc
// section 15.2/15.3).
func newClientError(resp *http.Response, raw []byte) *ClientError {
	ce := &ClientError{StatusCode: resp.StatusCode}
	if len(raw) > 0 {
		var body mlflowErrorBody
		if err := json.Unmarshal(raw, &body); err == nil && (body.ErrorCode != "" || body.Message != "") {
			ce.Code = body.ErrorCode
			ce.Message = truncate(body.Message, 512)
		}
	}
	if ce.Message == "" {
		ce.Message = fmt.Sprintf("mlflow request failed with status %d", resp.StatusCode)
	}
	ce.retryable = classifyStatusRetryable(resp.StatusCode)
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		ce.RetryAfter = parseRetryAfter(ra)
	}
	return ce
}

func classifyNetworkError(err error) error {
	// Any transport-level failure (DNS, connect, TLS handshake, timeout,
	// connection reset) is treated as retryable (design doc section
	// 10.2's "network timeout/reset") unless it's actually our own SSRF
	// guard rejecting the address — that is a configuration problem, not a
	// transient one, and retrying it endlessly would just hammer a
	// deliberately-blocked target.
	msg := err.Error()
	retryable := true
	if strings.Contains(msg, "private or local address") || strings.Contains(msg, "not in the allowed host list") ||
		strings.Contains(msg, "outside the allowed CIDR ranges") || strings.Contains(msg, "did not resolve") {
		retryable = false
	}
	return &ClientError{Message: "mlflow request failed: " + sanitizeNetErr(msg), retryable: retryable}
}

// sanitizeNetErr strips a *url.Error's redundant "Get \"https://host/path?
// query\":" prefix (which can carry credential-bearing query parameters or
// simply be noisy) down to the underlying cause.
func sanitizeNetErr(msg string) string {
	if idx := strings.LastIndex(msg, "\": "); idx >= 0 {
		return msg[idx+3:]
	}
	return msg
}

func classifyStatusRetryable(status int) bool {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return false
	case status >= 400 && status < 500:
		return false
	default:
		return false
	}
}

func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// redactID keeps only a short prefix of an identifier in error messages —
// used for IDs that might, in a misconfigured deployment, double as
// something more sensitive than a bare UUID.
func redactID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
