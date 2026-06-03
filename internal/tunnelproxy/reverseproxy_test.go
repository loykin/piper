package tunnelproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestServeReverseProxyAppliesPolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/foo"; got != want {
			t.Fatalf("upstream path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-Forwarded-Host"), "example.com"; got != want {
			t.Fatalf("upstream X-Forwarded-Host = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("X-Forwarded-Proto"), "http"; got != want {
			t.Fatalf("upstream X-Forwarded-Proto = %q, want %q", got, want)
		}
		http.Redirect(w, r, "/lab?token=upstream", http.StatusFound)
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/foo?token=req", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	policy := PolicyFunc{
		OnRequest: func(r *http.Request) error {
			SetForwardedHeaders(r, "example.com", "http")
			return nil
		},
		OnResponse: func(resp *http.Response) error {
			return RewriteLocationPrefix(resp, "/services/predict/demo", "token")
		},
	}

	ServeReverseProxy(rr, req, target, policy)

	if got, want := rr.Code, http.StatusFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Location"), "/services/predict/demo/lab"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
