package jupyter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildBaseURLIncludesProxyPrefix(t *testing.T) {
	// design doc §4.2: appending /api/... directly to Endpoint bypasses
	// Jupyter's --ServerApp.base_url and 404s. The base URL MUST include
	// the same /projects/{id}/notebooks/{name}/proxy prefix
	// pkg/notebook/dispatch/localdriver.Driver configures the Jupyter
	// process with.
	got := BuildBaseURL("http://127.0.0.1:8888", "proj-1", "nb-1")
	want := "http://127.0.0.1:8888/projects/proj-1/notebooks/nb-1/proxy"
	if got != want {
		t.Fatalf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestBuildWebSocketURLSwapsSchemeAndKeepsPrefix(t *testing.T) {
	got, err := BuildWebSocketURL("http://127.0.0.1:8888", "proj-1", "nb-1", "kernel-1", "sess-1")
	if err != nil {
		t.Fatalf("BuildWebSocketURL: %v", err)
	}
	want := "ws://127.0.0.1:8888/projects/proj-1/notebooks/nb-1/proxy/api/kernels/kernel-1/channels?session_id=sess-1"
	if got != want {
		t.Fatalf("BuildWebSocketURL = %q, want %q", got, want)
	}

	gotTLS, err := BuildWebSocketURL("https://notebooks.example.com", "proj-2", "nb-2", "kernel-2", "sess-2")
	if err != nil {
		t.Fatalf("BuildWebSocketURL (tls): %v", err)
	}
	wantTLS := "wss://notebooks.example.com/projects/proj-2/notebooks/nb-2/proxy/api/kernels/kernel-2/channels?session_id=sess-2"
	if gotTLS != wantTLS {
		t.Fatalf("BuildWebSocketURL (tls) = %q, want %q", gotTLS, wantTLS)
	}
}

// fakeJupyterServer records every request path it receives and serves
// canned Jupyter Contents/Sessions/Kernels API responses, enough to
// exercise Client's request building, auth header, and response decoding
// without a real Jupyter process (unavailable in this environment — see
// the test report).
func fakeJupyterServer(t *testing.T) (*httptest.Server, *[]string, *[]string) {
	return fakeJupyterServerWithXSRF(t, nil)
}

// fakeJupyterServerWithXSRF is fakeJupyterServer, plus xsrfHeaders (if
// non-nil) records the X-XSRFToken header seen on every state-changing
// (non-GET) request — see TestClientSendsXSRFTokenOnStateChangingRequests.
func fakeJupyterServerWithXSRF(t *testing.T, xsrfHeaders *[]string) (*httptest.Server, *[]string, *[]string) {
	t.Helper()
	var paths []string
	var authHeaders []string
	recordXSRF := func(r *http.Request) {
		if xsrfHeaders != nil && r.Method != http.MethodGet {
			*xsrfHeaders = append(*xsrfHeaders, r.Header.Get("X-XSRFToken"))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/contents/dir", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		recordXSRF(r)
		_ = json.NewEncoder(w).Encode(ContentModel{
			Name: "dir", Path: "dir", Type: "directory",
			Content: json.RawMessage(`[{"name":"a.ipynb","path":"dir/a.ipynb","type":"notebook"}]`),
		})
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/contents/dir/a.ipynb", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		recordXSRF(r)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(ContentModel{
			Name: "a.ipynb", Path: "dir/a.ipynb", Type: "notebook", Format: "json",
			Content: json.RawMessage(`{"nbformat":4,"nbformat_minor":5,"metadata":{},"cells":[]}`),
		})
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		recordXSRF(r)
		_ = json.NewEncoder(w).Encode(SessionModel{
			ID: "sess-1", Path: "dir/a.ipynb", Kernel: SessionKernel{ID: "kernel-1", Name: "python3", State: "idle"},
		})
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/kernels/kernel-1/interrupt", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		recordXSRF(r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/kernels/missing/interrupt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// Real Jupyter Server only sets its _xsrf cookie on an HTML page
	// response (a pure JSON API handler like /api/status does not render
	// the template that triggers it) — verified live against a real
	// jupyter_server; GET /lab is the page Piper's own driver always has
	// available, since it launches Jupyter via `jupyter lab`. ensureXSRFToken
	// (client.go) fetches this once per Client and echoes the cookie back
	// as X-XSRFToken on every non-GET request — without this route, every
	// PUT/POST/DELETE below fails exactly the way it did against a real
	// server before that fix existed.
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/lab", func(w http.ResponseWriter, r *http.Request) {
		// Path explicitly set to the proxy prefix *with* a trailing slash —
		// matching real Jupyter Server exactly (it scopes the cookie to its
		// configured --ServerApp.base_url, which always has a trailing
		// slash: see pkg/notebook/dispatch/localdriver.Driver's baseURL).
		// A cookie with no explicit Path (Go's http.SetCookie default) would
		// fall back to RFC 6265's request-path-minus-last-segment algorithm
		// instead, which for this request happens to produce the same
		// no-trailing-slash string ensureXSRFToken's old, buggy
		// Jar.Cookies(c.baseURL) call also used — the two bugs would have
		// silently canceled each other out and this test would have passed
		// even with the path-scoping bug still present. Setting Path
		// explicitly, the way the real server does, is what actually
		// exercises it.
		http.SetCookie(w, &http.Cookie{Name: "_xsrf", Value: "fake-xsrf-token", Path: "/projects/p1/notebooks/nb1/proxy/"})
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &paths, &authHeaders
}

func TestClientRoundTripsThroughProxyPrefix(t *testing.T) {
	srv, paths, authHeaders := fakeJupyterServer(t)
	base := BuildBaseURL(srv.URL, "p1", "nb1")
	c := NewClient(base, "secret-token")
	ctx := context.Background()

	dir, err := c.GetContents(ctx, "dir")
	if err != nil {
		t.Fatalf("GetContents(dir): %v", err)
	}
	if dir.Type != "directory" {
		t.Fatalf("GetContents(dir).Type = %q, want directory", dir.Type)
	}

	doc, err := c.GetContents(ctx, "dir/a.ipynb")
	if err != nil {
		t.Fatalf("GetContents(notebook): %v", err)
	}
	if doc.Type != "notebook" || len(doc.Content) == 0 {
		t.Fatalf("GetContents(notebook) = %#v, want a notebook with content", doc)
	}

	if err := c.PutContents(ctx, "dir/a.ipynb", ContentModel{Type: "notebook", Format: "json", Content: json.RawMessage(`{"nbformat":4}`)}); err != nil {
		t.Fatalf("PutContents: %v", err)
	}

	session, err := c.CreateSession(ctx, "dir/a.ipynb", "python3")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.Kernel.ID != "kernel-1" {
		t.Fatalf("CreateSession kernel id = %q, want kernel-1", session.Kernel.ID)
	}

	if err := c.InterruptKernel(ctx, "kernel-1"); err != nil {
		t.Fatalf("InterruptKernel: %v", err)
	}

	// every request must go through the /projects/p1/notebooks/nb1/proxy
	// prefix — a bare /api/... path would mean BuildBaseURL's prefix logic
	// regressed.
	for _, p := range *paths {
		const prefix = "/projects/p1/notebooks/nb1/proxy/api/"
		if len(p) < len(prefix) || p[:len(prefix)] != prefix {
			t.Errorf("request path %q did not go through the expected proxy prefix %q", p, prefix)
		}
	}
	// the token must be sent as an Authorization header, never a query
	// parameter (design doc §4.2 — query strings land in access logs).
	for _, h := range *authHeaders {
		if h != "token secret-token" {
			t.Errorf("Authorization header = %q, want %q", h, "token secret-token")
		}
	}
}

// TestClientSendsXSRFTokenOnStateChangingRequests is the regression for a
// bug found via live testing against a real jupyter_server (not just the
// httptest fake this package's other tests use): every non-GET Jupyter
// Server API call was rejected with 403 "'_xsrf' argument missing from
// POST" because the client never obtained or sent the required XSRF token
// — Jupyter's own auth token being disabled (Piper's normal deployment) has
// no bearing on this; Tornado's CSRF protection is unconditional. Confirmed
// live: a bare GET to a JSON API endpoint does not set the _xsrf cookie,
// but GET /lab (the page Piper's driver always has, since it launches
// Jupyter via `jupyter lab`) does — see ensureXSRFToken's doc comment in
// client.go for the fix and fakeJupyterServerWithXSRF's /lab route above
// for how this test reproduces that exact behavior instead of just
// asserting against a fake that never needed the fix in the first place.
func TestClientSendsXSRFTokenOnStateChangingRequests(t *testing.T) {
	var xsrfHeaders []string
	srv, _, _ := fakeJupyterServerWithXSRF(t, &xsrfHeaders)
	base := BuildBaseURL(srv.URL, "p1", "nb1")
	c := NewClient(base, "secret-token")
	ctx := context.Background()

	if err := c.PutContents(ctx, "dir/a.ipynb", ContentModel{Type: "notebook", Format: "json", Content: json.RawMessage(`{"nbformat":4}`)}); err != nil {
		t.Fatalf("PutContents: %v", err)
	}
	if _, err := c.CreateSession(ctx, "dir/a.ipynb", "python3"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := c.InterruptKernel(ctx, "kernel-1"); err != nil {
		t.Fatalf("InterruptKernel: %v", err)
	}

	if len(xsrfHeaders) != 3 {
		t.Fatalf("got %d state-changing requests recorded, want 3: %v", len(xsrfHeaders), xsrfHeaders)
	}
	for i, h := range xsrfHeaders {
		if h != "fake-xsrf-token" {
			t.Errorf("request %d: X-XSRFToken header = %q, want %q", i, h, "fake-xsrf-token")
		}
	}
}

func TestClientErrorDoesNotLeakEndpointOrToken(t *testing.T) {
	srv, _, _ := fakeJupyterServer(t)
	base := BuildBaseURL(srv.URL, "p1", "nb1")
	c := NewClient(base, "super-secret-token")

	err := c.InterruptKernel(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	status, ok := AsStatusError(err)
	if !ok || status != http.StatusNotFound {
		t.Fatalf("AsStatusError = (%d, %v), want (404, true)", status, ok)
	}
	if containsSubstring(err.Error(), "super-secret-token") {
		t.Fatalf("error message leaked the token: %q", err.Error())
	}
	if containsSubstring(err.Error(), srv.URL) {
		t.Fatalf("error message leaked the endpoint: %q", err.Error())
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
