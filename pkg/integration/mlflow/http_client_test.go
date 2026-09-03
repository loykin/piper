package mlflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestClient builds an httpClient against srv, bypassing NewHTTPClient's
// SSRF validation. httptest's 127.0.0.1 could be admitted through
// SSRFPolicy.AllowedHosts/AllowedCIDRs (see
// TestNewHTTPClient_AllowsPrivateTrackingURIWhenAllowlisted), but these
// wire-format/behavior tests aren't exercising the SSRF boundary itself, so
// this constructs the same *httpClient with a plain transport instead of
// threading a policy through every table-driven server case here.
func newTestClient(t *testing.T, srv *httptest.Server) Client {
	t.Helper()
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	return &httpClient{
		base:       base,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		token:      "test-token",
	}
}

func TestHTTPClient_GetExperimentByName_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2.0/mlflow/experiments/get-by-name" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("experiment_name") != "piper/p1/train" {
			t.Errorf("unexpected experiment_name %s", r.URL.Query().Get("experiment_name"))
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"experiment": map[string]any{"experiment_id": "1", "name": "piper/p1/train", "lifecycle_stage": "active"},
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	exp, err := c.GetExperimentByName(context.Background(), "piper/p1/train")
	if err != nil {
		t.Fatalf("GetExperimentByName: %v", err)
	}
	if exp == nil || exp.ExperimentID != "1" || exp.Name != "piper/p1/train" {
		t.Fatalf("GetExperimentByName = %+v", exp)
	}
}

func TestHTTPClient_GetExperimentByName_NotFoundReturnsNilNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error_code": "RESOURCE_DOES_NOT_EXIST", "message": "no experiment"})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	exp, err := c.GetExperimentByName(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetExperimentByName: err = %v, want nil (not-found is not an error)", err)
	}
	if exp != nil {
		t.Fatalf("GetExperimentByName = %+v, want nil", exp)
	}
}

func TestHTTPClient_CreateRun_SetsRunNameTagAndField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["run_name"] != "train-abcd1234" {
			t.Errorf("run_name field = %v, want train-abcd1234", body["run_name"])
		}
		tags, _ := body["tags"].([]any)
		found := false
		for _, tag := range tags {
			m, _ := tag.(map[string]any)
			if m["key"] == "mlflow.runName" && m["value"] == "train-abcd1234" {
				found = true
			}
		}
		if !found {
			t.Errorf("mlflow.runName tag not set: tags=%v", tags)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{"info": map[string]any{"run_id": "run-1", "experiment_id": "1", "status": "RUNNING"}},
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	run, err := c.CreateRun(context.Background(), CreateRunRequest{ExperimentID: "1", RunName: "train-abcd1234", StartTime: 1000})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("CreateRun = %+v", run)
	}
}

func TestHTTPClient_LogBatch(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	err := c.LogBatch(context.Background(), LogBatchRequest{
		RunID:  "run-1",
		Params: []Param{{Key: "epochs", Value: "10"}},
		Tags:   []Tag{{Key: "piper.run_id", Value: "r1"}},
	})
	if err != nil {
		t.Fatalf("LogBatch: %v", err)
	}
	if gotBody["run_id"] != "run-1" {
		t.Fatalf("run_id = %v, want run-1", gotBody["run_id"])
	}
}

func TestHTTPClient_UpdateRun(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	err := c.UpdateRun(context.Background(), UpdateRunRequest{RunID: "run-1", Status: RunStatusFinished, EndTime: 2000})
	if err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	if gotBody["status"] != "FINISHED" {
		t.Fatalf("status = %v, want FINISHED", gotBody["status"])
	}
}

func TestHTTPClient_SearchRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["filter"] != "tags.piper.run_id = 'r1'" {
			t.Errorf("filter = %v", body["filter"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs": []map[string]any{{"info": map[string]any{"run_id": "run-1"}}},
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	page, err := c.SearchRuns(context.Background(), SearchRunsRequest{ExperimentIDs: []string{"1"}, Filter: "tags.piper.run_id = 'r1'", MaxResults: 1})
	if err != nil {
		t.Fatalf("SearchRuns: %v", err)
	}
	if len(page.Runs) != 1 || page.Runs[0].RunID != "run-1" {
		t.Fatalf("SearchRuns = %+v", page)
	}
}

// --- retry classification / error redaction ---

func TestHTTPClient_5xxIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error_code":"TEMPORARILY_UNAVAILABLE","message":"try later"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.GetExperimentByName(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsRetryable(err) {
		t.Errorf("503 should be retryable: %v", err)
	}
}

func TestHTTPClient_401IsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error_code":"UNAUTHENTICATED","message":"bad token"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.GetExperimentByName(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsRetryable(err) {
		t.Errorf("401 should not be retryable: %v", err)
	}
	if ErrorCode(err) != "UNAUTHENTICATED" {
		t.Errorf("ErrorCode = %q, want UNAUTHENTICATED", ErrorCode(err))
	}
}

func TestHTTPClient_429RetryAfterHonored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error_code":"RATE_LIMITED","message":"slow down"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.GetExperimentByName(context.Background(), "x")
	var ce *ClientError
	if !isClientErr(err, &ce) {
		t.Fatalf("expected *ClientError, got %T: %v", err, err)
	}
	if !ce.Retryable() {
		t.Errorf("429 should be retryable")
	}
	if ce.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", ce.RetryAfter)
	}
}

func TestHTTPClient_ErrorMessageNeverLeaksRawBodyOrCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A misconfigured proxy/error page: not MLflow's JSON envelope,
		// and — if this ever leaked — would prove a credential-in-body
		// reflection risk.
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html><body>Bearer test-token leaked here, upstream stack trace: ...huge HTML...</body></html>`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.GetExperimentByName(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Errorf("error message leaked the credential: %q", err.Error())
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Errorf("error message leaked the raw response body: %q", err.Error())
	}
}

func TestHTTPClient_ResponseSizeIsBounded(t *testing.T) {
	huge := strings.Repeat("a", maxResponseBytes*2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"experiment":{"experiment_id":"1","name":"` + huge + `"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	// The response is truncated mid-JSON by maxResponseBytes, so decoding
	// fails — the important assertion is that this returns a bounded,
	// redacted error rather than buffering gigabytes or panicking.
	_, err := c.GetExperimentByName(context.Background(), "x")
	if err == nil {
		t.Fatal("expected a decode error from the truncated oversized response")
	}
	if len(err.Error()) > 1024 {
		t.Errorf("error message itself is unexpectedly large (%d bytes) — response bound may have leaked into it", len(err.Error()))
	}
}

func TestNewHTTPClient_RejectsPrivateTrackingURI(t *testing.T) {
	_, err := NewHTTPClient(HTTPClientConfig{TrackingURI: "https://192.168.1.10:5000", Policy: DefaultSSRFPolicy()})
	if err == nil {
		t.Fatal("expected NewHTTPClient to reject a private-IP tracking URI")
	}
}

// TestNewHTTPClient_AllowsPrivateTrackingURIWhenAllowlisted covers the AF
// finding (adversarial-qa-2026-08-31.md / -09-02.md): a self-hosted MLflow
// server living at a private/loopback address (the common self-hosted
// deployment shape) must be reachable once an admin explicitly trusts it
// via AllowedHosts or AllowedCIDRs — both the write-time ValidateTrackingURI
// check and newSafeTransport's dial-time re-check must honor that trust,
// not just the URL parse.
func TestNewHTTPClient_AllowsPrivateTrackingURIWhenAllowlisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"experiment": map[string]any{"experiment_id": "1", "name": "x", "lifecycle_stage": "active"},
		})
	}))
	defer srv.Close()
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}

	t.Run("AllowedHosts", func(t *testing.T) {
		c, err := NewHTTPClient(HTTPClientConfig{
			TrackingURI: srv.URL,
			Policy: SSRFPolicy{
				AllowInsecureHTTP: true,
				AllowedHosts:      []string{srvURL.Hostname()},
			},
		})
		if err != nil {
			t.Fatalf("NewHTTPClient: %v", err)
		}
		if _, err := c.GetExperimentByName(context.Background(), "x"); err != nil {
			t.Fatalf("GetExperimentByName: %v", err)
		}
	})

	t.Run("AllowedCIDRs", func(t *testing.T) {
		c, err := NewHTTPClient(HTTPClientConfig{
			TrackingURI: srv.URL,
			Policy: SSRFPolicy{
				AllowInsecureHTTP: true,
				AllowedCIDRs:      []string{srvURL.Hostname() + "/32"},
			},
		})
		if err != nil {
			t.Fatalf("NewHTTPClient: %v", err)
		}
		if _, err := c.GetExperimentByName(context.Background(), "x"); err != nil {
			t.Fatalf("GetExperimentByName: %v", err)
		}
	})

	t.Run("StillRejectedWithoutAllowlist", func(t *testing.T) {
		_, err := NewHTTPClient(HTTPClientConfig{
			TrackingURI: srv.URL,
			Policy:      SSRFPolicy{AllowInsecureHTTP: true},
		})
		if err == nil {
			t.Fatal("expected NewHTTPClient to reject a private-IP tracking URI with no allowlist")
		}
	})
}

func TestNewHTTPClient_RejectsTokenAndBasicAuthTogether(t *testing.T) {
	_, err := NewHTTPClient(HTTPClientConfig{
		TrackingURI: "https://mlflow.example.com",
		Token:       "tok",
		Username:    "u",
		Password:    "p",
	})
	if err == nil {
		t.Fatal("expected NewHTTPClient to reject token + username/password together")
	}
}

func isClientErr(err error, out **ClientError) bool {
	ce, ok := err.(*ClientError)
	if ok {
		*out = ce
	}
	return ok
}
