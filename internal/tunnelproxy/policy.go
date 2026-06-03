package tunnelproxy

import (
	"net/http"
	"net/url"
	"strings"
)

// PolicyFunc is a convenience implementation for composing request/response
// rewrite hooks without defining a new struct for every backend.
type PolicyFunc struct {
	OnRequest  func(*http.Request) error
	OnResponse func(*http.Response) error
}

func (p PolicyFunc) RewriteRequest(req *http.Request) error {
	if p.OnRequest != nil {
		return p.OnRequest(req)
	}
	return nil
}

func (p PolicyFunc) RewriteResponse(resp *http.Response) error {
	if p.OnResponse != nil {
		return p.OnResponse(resp)
	}
	return nil
}

// Chain combines multiple policies. Request hooks run in order; response hooks
// run in reverse order so the outermost policy sees the response last.
func Chain(policies ...Policy) Policy {
	nonNil := make([]Policy, 0, len(policies))
	for _, p := range policies {
		if p != nil {
			nonNil = append(nonNil, p)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return chainPolicy(nonNil)
}

type chainPolicy []Policy

func (p chainPolicy) RewriteRequest(req *http.Request) error {
	for _, policy := range p {
		if err := policy.RewriteRequest(req); err != nil {
			return err
		}
	}
	return nil
}

func (p chainPolicy) RewriteResponse(resp *http.Response) error {
	for i := len(p) - 1; i >= 0; i-- {
		if err := p[i].RewriteResponse(resp); err != nil {
			return err
		}
	}
	return nil
}

// RewriteLocationPrefix rewrites an upstream Location header so it stays on the
// current proxy prefix. Query parameters matching stripKeys are removed.
func RewriteLocationPrefix(resp *http.Response, proxyPrefix string, stripKeys ...string) error {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil
	}
	u, err := url.Parse(loc)
	if err != nil {
		return nil
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasPrefix(path, proxyPrefix) {
		resp.Header.Set("Location", pathWithQuery(path, stripQueryKeys(u.RawQuery, stripKeys...)))
		return nil
	}
	resp.Header.Set("Location", pathWithQuery(strings.TrimRight(proxyPrefix, "/")+path, stripQueryKeys(u.RawQuery, stripKeys...)))
	return nil
}

func pathWithQuery(path, query string) string {
	if query == "" {
		return path
	}
	return path + "?" + query
}

func stripQueryKeys(rawQuery string, keys ...string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	for _, key := range keys {
		values.Del(key)
	}
	return values.Encode()
}
