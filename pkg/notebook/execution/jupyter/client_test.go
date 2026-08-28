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
	t.Helper()
	var paths []string
	var authHeaders []string
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/contents/dir", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(ContentModel{
			Name: "dir", Path: "dir", Type: "directory",
			Content: json.RawMessage(`[{"name":"a.ipynb","path":"dir/a.ipynb","type":"notebook"}]`),
		})
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/contents/dir/a.ipynb", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
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
		_ = json.NewEncoder(w).Encode(SessionModel{
			ID: "sess-1", Path: "dir/a.ipynb", Kernel: SessionKernel{ID: "kernel-1", Name: "python3", State: "idle"},
		})
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/kernels/kernel-1/interrupt", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/projects/p1/notebooks/nb1/proxy/api/kernels/missing/interrupt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
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
