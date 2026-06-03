package tunnelproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testPolicy struct {
	t            *testing.T
	requestSeen  bool
	responseSeen bool
}

func (p *testPolicy) RewriteRequest(req *http.Request) error {
	p.requestSeen = true
	req.Header.Set("X-Policy", "ok")
	req.URL.RawQuery = "a=1"
	return nil
}

func (p *testPolicy) RewriteResponse(resp *http.Response) error {
	p.responseSeen = true
	resp.Header.Set("X-Resp", "yes")
	return nil
}

func TestServeHTTPForwardsHTTPResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/notebooks/demo/proxy/lab", nil)
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	go func() {
		defer func() { _ = server.Close() }()
		gotReq, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			t.Errorf("ReadRequest error: %v", err)
			return
		}
		if got, want := gotReq.Header.Get("X-Policy"), "ok"; got != want {
			t.Errorf("request header X-Policy = %q, want %q", got, want)
		}
		if got, want := gotReq.URL.RawQuery, "a=1"; got != want {
			t.Errorf("request query = %q, want %q", got, want)
		}
		if !gotReq.Close {
			t.Errorf("request Close = false, want true")
		}
		_, _ = fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nX-Test: ok\r\nContent-Length: 5\r\n\r\nhello")
	}()

	rr := httptest.NewRecorder()
	policy := &testPolicy{t: t}
	if err := ServeHTTP(rr, req, client, policy); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("X-Test"), "ok"; got != want {
		t.Fatalf("header X-Test = %q, want %q", got, want)
	}
	if got, want := rr.Header().Get("X-Resp"), "yes"; got != want {
		t.Fatalf("header X-Resp = %q, want %q", got, want)
	}
	if got, want := rr.Body.String(), "hello"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if !policy.requestSeen {
		t.Fatalf("request policy was not called")
	}
	if !policy.responseSeen {
		t.Fatalf("response policy was not called")
	}
}

type hijackResponseWriter struct {
	header http.Header
	conn   net.Conn
}

func (w *hijackResponseWriter) Header() http.Header { return w.header }
func (w *hijackResponseWriter) WriteHeader(int)     {}
func (w *hijackResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestServeHTTPForwardsWebSocketHandshakeAndFrames(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/notebooks/demo/proxy/api/kernels/x/channels", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade")

	upstreamClient, upstreamServer := net.Pipe()
	defer func() { _ = upstreamClient.Close() }()

	downstreamClient, downstreamServer := net.Pipe()
	defer func() { _ = downstreamClient.Close() }()

	go func() {
		defer func() { _ = upstreamServer.Close() }()
		if _, err := http.ReadRequest(bufio.NewReader(upstreamServer)); err != nil {
			t.Errorf("ReadRequest error: %v", err)
			return
		}
		_, _ = fmt.Fprint(upstreamServer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_, _ = fmt.Fprint(upstreamServer, "srv-msg")
	}()

	rr := &hijackResponseWriter{header: make(http.Header), conn: downstreamServer}
	policy := &testPolicy{t: t}
	gotCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := io.ReadAll(downstreamClient)
		if err != nil {
			errCh <- err
			return
		}
		gotCh <- string(got)
	}()
	if err := ServeHTTP(rr, req, upstreamClient, policy); err != nil {
		t.Fatalf("ServeHTTP websocket error: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ReadAll error: %v", err)
	case got := <-gotCh:
		if !containsAll(got, []string{"101 Switching Protocols", "Upgrade: websocket", "X-Resp: yes", "srv-msg"}) {
			t.Fatalf("downstream data = %q", got)
		}
	}
	if !policy.requestSeen {
		t.Fatalf("request policy was not called for websocket")
	}
	if !policy.responseSeen {
		t.Fatalf("response policy was not called for websocket")
	}
}

func containsAll(s string, want []string) bool {
	for _, sub := range want {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
