package serving

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyServeHTTPUsesSharedProxyHelpers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("X-Forwarded-Host"), "example.com"; got != want {
			t.Fatalf("X-Forwarded-Host = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/foo"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		http.Redirect(w, r, "/results", http.StatusFound)
	}))
	defer upstream.Close()

	repo := newStubServingRepo(&Service{Name: "demo", Status: StatusRunning, Endpoint: upstream.URL})
	proxy := NewProxy(repo)

	req := httptest.NewRequest(http.MethodGet, "/services/predict/demo/foo", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Location"), "/services/predict/demo/results"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
