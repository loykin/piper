package serving

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/piper/piper/internal/tunnelproxy"
	"github.com/piper/piper/pkg/project"
)

func init() {
	tunnelproxy.RegisterPolicy("serving", func(ctx tunnelproxy.PolicyContext) tunnelproxy.Policy {
		return tunnelproxy.PolicyFunc{
			OnRequest: func(req *http.Request) error {
				tunnelproxy.SetForwardedHeaders(req, ctx.Host, ctx.Scheme)
				return nil
			},
			OnResponse: func(resp *http.Response) error {
				return tunnelproxy.RewriteLocationPrefix(resp, ctx.ProxyPrefix)
			},
		}
	})
}

// Proxy forwards inference requests to the appropriate serving endpoint.
// It is mounted at /services/predict/{name} by the API handler.
type Proxy struct {
	repo Repository
}

// NewProxy creates a Proxy backed by the given repository.
func NewProxy(repo Repository) *Proxy {
	return &Proxy{repo: repo}
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

	projectContext, _ := project.FromContext(r.Context())
	svc, err := p.repo.Get(r.Context(), projectContext.ID, name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if svc == nil || svc.Status != StatusRunning {
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

	policy, err := tunnelproxy.BuildPolicy("serving", tunnelproxy.PolicyContext{
		Request:     r,
		Name:        name,
		Host:        r.Host,
		Scheme:      tunnelproxy.RequestScheme(r),
		ProxyPrefix: "/services/predict/" + name,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tunnelproxy.ServeReverseProxy(w, r2, target, policy)
}
