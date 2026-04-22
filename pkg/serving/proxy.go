package serving

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/piper/piper/pkg/store"
)

// Proxy forwards inference requests to the appropriate serving endpoint.
// It is mounted at /services/predict/{name} by the API handler.
type Proxy struct {
	store *store.Store
}

// NewProxy creates a Proxy backed by the given store.
func NewProxy(st *store.Store) *Proxy {
	return &Proxy{store: st}
}

// ServeHTTP handles POST /services/predict/{name}[/...].
// It looks up the service endpoint and reverse-proxies the request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract service name from path: /services/predict/{name}[/extra...]
	rest := strings.TrimPrefix(r.URL.Path, "/services/predict/")
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}

	svc, err := p.store.GetService(name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if svc == nil || svc.Status != store.ServiceStatusRunning {
		http.Error(w, "service not found or not running", http.StatusServiceUnavailable)
		return
	}

	target, err := url.Parse(svc.Endpoint)
	if err != nil {
		http.Error(w, "invalid service endpoint", http.StatusInternalServerError)
		return
	}

	// Strip the /services/predict/{name} prefix so the runtime sees the original sub-path.
	subPath := "/"
	if len(parts) == 2 {
		subPath = "/" + parts[1]
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = subPath
	r2.URL.RawPath = ""

	httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r2)
}
