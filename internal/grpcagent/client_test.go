package grpcagent

import (
	"net"
	"testing"
	"time"
)

func TestClientProxySessionBuffersDataBeforeAttach(t *testing.T) {
	session := newClientProxySession()
	session.send([]byte("GET / HTTP/1.1\r\n\r\n"))

	target, worker := net.Pipe()
	defer func() { _ = target.Close() }()

	if !session.attach(worker) {
		t.Fatal("attach returned false")
	}
	go session.writeToTarget()
	defer session.close()

	_ = target.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	n, err := target.Read(buf)
	if err != nil {
		t.Fatalf("read buffered proxy data: %v", err)
	}
	if got, want := string(buf[:n]), "GET / HTTP/1.1\r\n\r\n"; got != want {
		t.Fatalf("buffered data mismatch: got %q want %q", got, want)
	}
}

func TestClientProxySessionAttachAfterCloseClosesConn(t *testing.T) {
	session := newClientProxySession()
	session.close()

	target, worker := net.Pipe()
	defer func() { _ = target.Close() }()

	if session.attach(worker) {
		t.Fatal("attach returned true for closed session")
	}

	_ = target.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := target.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected remote read to fail after attach closes conn")
	}
}
