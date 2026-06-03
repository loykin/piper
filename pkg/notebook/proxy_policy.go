package notebook

import (
	"net/http"

	"github.com/piper/piper/internal/tunnelproxy"
)

func init() {
	tunnelproxy.RegisterPolicy("notebook", func(ctx tunnelproxy.PolicyContext) tunnelproxy.Policy {
		return tunnelproxy.PolicyFunc{
			OnRequest: func(req *http.Request) error {
				req.URL.RawQuery = tunnelproxy.InjectQueryParamUnlessCookie(req, "token", ctx.Token, "username-")
				tunnelproxy.SetForwardedHeaders(req, ctx.Host, ctx.Scheme)
				return nil
			},
			OnResponse: func(resp *http.Response) error {
				return tunnelproxy.RewriteLocationPrefix(resp, ctx.ProxyPrefix, "token")
			},
		}
	})
}
