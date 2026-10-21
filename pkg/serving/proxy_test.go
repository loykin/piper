package serving

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestProxyThroughProjectRoutePreservesPathAndRedirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model.json" || r.URL.RawQuery != "revision=1" {
			t.Errorf("upstream URL = %s, want /model.json?revision=1", r.URL)
		}
		http.Redirect(w, r, "/results", http.StatusFound)
	}))
	defer upstream.Close()
	repo := newStubServingRepo(&Service{Name: "demo", Status: StatusRunning, Endpoint: upstream.URL})
	router := gin.New()
	NewHandler(HandlerDeps{Proxy: NewProxy(repo)}).RegisterProxyRoutes(router.Group("/projects/:project_id"))
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get(server.URL + "/projects/proj/services/predict/demo/model.json?revision=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/projects/proj/services/predict/demo/results" {
		t.Fatalf("Location = %q, want project-scoped redirect", got)
	}
}
