package grpcagent

import (
	"io"
	"testing"
	"time"
)

func TestWorkerConnDeliverProxyDataDoesNotBlockBeforePipeRead(t *testing.T) {
	_, pw := io.Pipe()
	pc := &proxyChannel{incoming: make(chan []byte, 1024), pw: pw}
	conn := newWorkerConn("agent-1", nil)
	conn.proxyChannels.Store("ch-1", pc)

	go func() {
		for data := range pc.incoming {
			if _, err := pw.Write(data); err != nil {
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		conn.deliverProxyData("ch-1", []byte("HTTP/1.1 200 OK\r\n\r\n"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("deliverProxyData blocked while pipe reader was not ready")
	}

	proxyChannelClose(pc.incoming)
	_ = pw.Close()
}
