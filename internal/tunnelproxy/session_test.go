package tunnelproxy

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionServeHTTPAndClose(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	go func() {
		defer func() { _ = server.Close() }()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
			t.Errorf("ReadRequest error: %v", err)
			return
		}
		_, _ = server.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
	}()

	session := NewBuilder(client).Build()
	rr := httptest.NewRecorder()
	if err := session.ServeHTTP(rr, req); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if got, want := rr.Code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close error: %v", err)
	}
	if err := session.ServeHTTP(rr, req); err == nil {
		t.Fatalf("ServeHTTP after Close = nil error, want non-nil")
	}
}

func TestSessionManagerTracksAndClosesAll(t *testing.T) {
	mgr := NewManager()
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()
	session := mgr.Open("demo", left, nil)
	if session == nil {
		t.Fatal("session = nil")
	}
	if got, ok := mgr.Get("demo"); !ok || got != session {
		t.Fatalf("manager get = %v, %v want tracked session", got, ok)
	}
	if err := mgr.CloseAll(); err != nil {
		t.Fatalf("CloseAll error: %v", err)
	}
	if _, ok := mgr.Get("demo"); ok {
		t.Fatal("manager retained session after CloseAll")
	}
}
