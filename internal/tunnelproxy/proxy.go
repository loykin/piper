package tunnelproxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// Policy customizes how a proxied request/response is adapted for the upstream
// service.
type Policy interface {
	RewriteRequest(*http.Request) error
	RewriteResponse(*http.Response) error
}

// ServeHTTP forwards a single HTTP or WebSocket request over an already-open
// upstream connection. Regular HTTP responses are read and written via the
// supplied ResponseWriter; WebSocket upgrades are hijacked and piped raw.
func ServeHTTP(w http.ResponseWriter, req *http.Request, upstream net.Conn, policy Policy) error {
	upgrade := isWebSocketUpgrade(req)
	reader := bufio.NewReader(upstream)
	if policy != nil {
		if err := policy.RewriteRequest(req); err != nil {
			return err
		}
	}
	if !upgrade {
		req.Close = true
	}
	if err := req.Write(upstream); err != nil {
		return err
	}
	if upgrade {
		return serveWebSocket(w, req, reader, upstream, policy)
	}
	return serveHTTP(w, req, reader, policy)
}

func serveHTTP(w http.ResponseWriter, req *http.Request, reader *bufio.Reader, policy Policy) error {
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if policy != nil {
		if err := policy.RewriteResponse(resp); err != nil {
			return err
		}
	}

	for k, vv := range resp.Header {
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

func serveWebSocket(w http.ResponseWriter, req *http.Request, reader *bufio.Reader, upstream net.Conn, policy Policy) error {
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if policy != nil {
		if err := policy.RewriteResponse(resp); err != nil {
			return err
		}
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("connection hijack not supported")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := resp.Write(conn); err != nil {
		return err
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, reader); done <- struct{}{} }()
	<-done
	return nil
}

func isHopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
