package grpcagent

import (
	"context"
	"io"
	"sync"
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

func TestWorkerConnPushLoopProcessesMessagesInOrder(t *testing.T) {
	conn := newWorkerConn("agent-1", nil)
	releaseFirst := make(chan struct{})
	seenSecond := make(chan struct{})
	var mu sync.Mutex
	got := make([]string, 0, 2)

	go conn.runPushLoop(func(ctx context.Context, agentID, method string, payload []byte) {
		mu.Lock()
		got = append(got, method)
		mu.Unlock()
		if method == "one" {
			<-releaseFirst
		}
		if method == "two" {
			select {
			case <-seenSecond:
			default:
				close(seenSecond)
			}
		}
	})

	if !conn.pushQueue.enqueue(pushMessage{method: "one"}) {
		t.Fatal("enqueue one failed")
	}
	if !conn.pushQueue.enqueue(pushMessage{method: "two"}) {
		t.Fatal("enqueue two failed")
	}

	select {
	case <-seenSecond:
		t.Fatal("second push was processed before the first completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-seenSecond:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second push was not processed")
	}

	conn.pushQueue.close()
	<-conn.pushDone

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("push order = %v, want [one two]", got)
	}
}

func TestUnregisterReplacedConnectionDoesNotReportAgentLost(t *testing.T) {
	lost := make(chan string, 1)
	s := NewServer(nil, func(agentID string) { lost <- agentID })
	oldConn := newWorkerConn("agent-1", nil)
	newConn := newWorkerConn("agent-1", nil)
	go oldConn.runPushLoop(nil)
	go newConn.runPushLoop(nil)

	s.register(oldConn)
	s.register(newConn)
	s.unregister("agent-1", oldConn)

	select {
	case id := <-lost:
		t.Fatalf("replaced connection reported agent %q lost", id)
	case <-time.After(50 * time.Millisecond):
	}
	if got := s.conns["agent-1"]; got != newConn {
		t.Fatal("new connection was removed by old connection cleanup")
	}

	s.unregister("agent-1", newConn)
	select {
	case id := <-lost:
		if id != "agent-1" {
			t.Fatalf("lost agent = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("active connection cleanup did not report agent lost")
	}
}
