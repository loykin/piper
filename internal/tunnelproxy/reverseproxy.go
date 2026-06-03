package tunnelproxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ServeReverseProxy forwards an HTTP request to a target URL while applying the
// same policy hooks used by the tunnel transport.
func ServeReverseProxy(w http.ResponseWriter, req *http.Request, target *url.URL, policy Policy) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = joinReverseProxyPath(target.Path, req.URL.Path)
			pr.Out.URL.RawPath = ""
			if policy != nil {
				_ = policy.RewriteRequest(pr.Out)
			}
		},
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if policy != nil {
			if err := policy.RewriteResponse(resp); err != nil {
				return err
			}
		}
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
		http.Error(rw, err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, req)
}

func joinReverseProxyPath(basePath, reqPath string) string {
	if basePath == "" {
		return reqPath
	}
	if reqPath == "" {
		reqPath = "/"
	}
	if !strings.HasPrefix(reqPath, "/") {
		reqPath = "/" + reqPath
	}
	return strings.TrimRight(basePath, "/") + reqPath
}
