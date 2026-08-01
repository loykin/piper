package grpcagent

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestDialProxyContextCancellationTearsDownChannel(t *testing.T) {
	stream := &fakeServerStream{}
	conn := newWorkerConn("agent-1", stream)
	srv := &Server{conns: map[string]*workerConn{"agent-1": conn}}

	ctx, cancel := context.WithCancel(context.Background())
	c, err := srv.DialProxy(ctx, "agent-1", "example:80")
	if err != nil {
		t.Fatalf("DialProxy error: %v", err)
	}
	pconn := c.(*proxyConn)
	if _, ok := conn.proxyChannels.Load(pconn.channelID); !ok {
		t.Fatal("proxy channel was not registered")
	}

	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := conn.proxyChannels.Load(pconn.channelID); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy channel was not torn down after ctx cancellation")
}

func newTestProxyConn(t *testing.T) (*proxyConn, *fakeServerStream) {
	t.Helper()
	stream := &fakeServerStream{}
	conn := newWorkerConn("agent-1", stream)
	pr, pw := io.Pipe()
	pc := &proxyChannel{incoming: make(chan []byte, 1), pw: pw}
	return &proxyConn{channelID: "chan-1", wconn: conn, pc: pc, pr: pr, closed: make(chan struct{})}, stream
}

func TestProxyConnReadDeadlineUnblocksRead(t *testing.T) {
	pc, _ := newTestProxyConn(t)
	defer func() { _ = pc.Close() }()

	if err := pc.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline error: %v", err)
	}
	buf := make([]byte, 10)
	_, err := pc.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read error = %v, want ErrDeadlineExceeded", err)
	}
}

func TestProxyConnWriteDeadlineFailsFastAfterExpiry(t *testing.T) {
	pc, _ := newTestProxyConn(t)
	defer func() { _ = pc.Close() }()

	if err := pc.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline error: %v", err)
	}
	_, err := pc.Write([]byte("hello"))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write error = %v, want ErrDeadlineExceeded", err)
	}
}
