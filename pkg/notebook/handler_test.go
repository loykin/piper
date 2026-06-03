package notebook

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInjectNotebookTokenInjectsWhenNoCookie(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab/?foo=bar", nil)
	got := injectNotebookToken(req, "tok-123")
	if got != "foo=bar&token=tok-123" {
		t.Fatalf("query = %q", got)
	}
}

func TestInjectNotebookTokenSkipsWhenCookiePresent(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab/", nil)
	req.AddCookie(&http.Cookie{Name: "username-127-0-0-1-8888", Value: "x"})
	got := injectNotebookToken(req, "tok-123")
	if got != "" {
		t.Fatalf("query = %q, want empty", got)
	}
}

func TestInjectNotebookTokenNoOpWhenTokenEmpty(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab/?foo=bar", nil)
	got := injectNotebookToken(req, "")
	if got != "foo=bar" {
		t.Fatalf("query = %q", got)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/api/kernels/x/channels", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade")

	if !isWebSocketUpgrade(req) {
		t.Fatal("expected websocket upgrade")
	}
}

func TestIsWebSocketUpgradeRejectsNormalHTTP(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab", nil)
	if err != nil {
		t.Fatal(err)
	}
	if isWebSocketUpgrade(req) {
		t.Fatal("normal HTTP request was treated as websocket")
	}
}

func TestNormalizeNotebookProxySubPath(t *testing.T) {
	cases := map[string]string{
		"":             "/",
		"lab":          "/lab",
		"/lab":         "/lab",
		"/api/kernels": "/api/kernels",
	}
	for in, want := range cases {
		if got := normalizeNotebookProxySubPath(in); got != want {
			t.Fatalf("normalizeNotebookProxySubPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewriteNotebookRedirectLocation(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "http://127.0.0.1:8888/lab?token=upstream")
	if err := rewriteNotebookRedirectLocation(resp, "jj"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}

	resp = &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "/lab")
	if err := rewriteNotebookRedirectLocation(resp, "jj"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRewriteNotebookRedirectLocationPreservesProxyPath(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "http://127.0.0.1:8888/notebooks/jj/proxy/lab/?token=upstream")
	if err := rewriteNotebookRedirectLocation(resp, "jj"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab/"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestSchemeFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if got, want := schemeFromRequest(req), "http"; got != want {
		t.Fatalf("schemeFromRequest(http) = %q, want %q", got, want)
	}
	req = httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.TLS = &tls.ConnectionState{}
	if got, want := schemeFromRequest(req), "https"; got != want {
		t.Fatalf("schemeFromRequest(https) = %q, want %q", got, want)
	}
}
