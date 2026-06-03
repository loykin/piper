package testutil

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// Server is a minimal test server wrapper that binds to 127.0.0.1 only.
type Server struct {
	URL string

	ln  net.Listener
	srv *http.Server
}

func (s *Server) ListenerAddr() net.Addr {
	return s.ln.Addr()
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	return s.ln.Close()
}

// NewIPv4Server starts an HTTP server bound to 127.0.0.1 only.
// This avoids environments where binding to ::1 is blocked.
func NewIPv4Server(t *testing.T, handler http.Handler) *Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp4: %v", err)
	}
	srv := &http.Server{Handler: handler}
	server := &Server{
		URL: "http://" + ln.Addr().String(),
		ln:  ln,
		srv: srv,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() { _ = server.Close() })
	return server
}
