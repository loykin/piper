package grpcagent

import (
	"io"
	"testing"
)

func TestProxyChannelSendClosesChannelOnOverflowInsteadOfDropping(t *testing.T) {
	stream := &fakeServerStream{}
	conn := newWorkerConn("agent-1", stream)
	_, pw := io.Pipe()
	pc := &proxyChannel{incoming: make(chan []byte, 1), pw: pw}
	conn.proxyChannels.Store("chan-1", pc)

	conn.deliverProxyData("chan-1", []byte("first"))  // fills the 1-item buffer
	conn.deliverProxyData("chan-1", []byte("second")) // overflow

	if _, ok := conn.proxyChannels.Load("chan-1"); ok {
		t.Fatal("proxy channel should have been torn down on overflow, not left dangling")
	}

	var found bool
	for _, m := range stream.sentMessages() {
		if pc := m.GetProxyClose(); pc != nil && pc.ChannelId == "chan-1" {
			if pc.Error == "" {
				t.Fatalf("ProxyClose for chan-1 has no error, want an explicit backpressure error")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected an explicit ProxyClose to be sent to the worker on overflow")
	}
}

func TestClientProxySessionOverflowClosesInsteadOfDropping(t *testing.T) {
	session := newClientProxySession()
	defer session.close()

	capacity := cap(session.incoming)
	for i := 0; i < capacity; i++ {
		if !session.send([]byte{byte(i)}) {
			t.Fatalf("send %d unexpectedly failed before the buffer filled", i)
		}
	}

	if session.send([]byte("overflow")) {
		t.Fatal("send reported success on overflow — data would be silently dropped instead of failing loudly")
	}
}
