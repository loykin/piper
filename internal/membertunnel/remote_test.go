package membertunnel

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

func TestRemoteHTTPBackpressureIsolatedToOneStream(t *testing.T) {
	r := newRemoteMemberClient("member-1", "test-token", func(*agentpb.HomeMessage) error { return nil })
	frames := make(chan *agentpb.MemberHTTPStreamData, 1)
	frames <- &agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("first")}
	r.streams = map[string]chan *agentpb.MemberHTTPStreamData{"slow": frames}

	done := make(chan struct{})
	go func() {
		r.deliverHTTP(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("overflow")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("full HTTP stream blocked the tunnel receive loop")
	}
	r.mu.Lock()
	_, retained := r.streams["slow"]
	r.mu.Unlock()
	if retained {
		t.Fatal("overflowed HTTP stream remained registered")
	}
}

func TestMemberHTTPBackpressureIsolatedToOneStream(t *testing.T) {
	c := NewClient(Config{}, &fakeMember{})
	frames := make(chan *agentpb.MemberHTTPStreamData, 1)
	frames <- &agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("first")}
	c.httpIn["slow"] = frames

	done := make(chan struct{})
	go func() {
		c.deliverHTTP(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("overflow")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("full HTTP stream blocked the tunnel receive loop")
	}
	c.httpMu.Lock()
	_, retained := c.httpIn["slow"]
	c.httpMu.Unlock()
	if retained {
		t.Fatal("overflowed HTTP stream remained registered")
	}
}

func TestCallRemovesPendingRequestOnContextCancellation(t *testing.T) {
	r := newRemoteMemberClient("member-1", "test-token", func(*agentpb.HomeMessage) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := call[string, memberclient.RunDetail](ctx, r, MethodGetRun, memberclient.AuthContext{}, project.ProjectRef{}, "run-1")
	if err == nil {
		t.Fatal("call succeeded without a response")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) != 0 {
		t.Fatalf("pending requests = %d, want 0", len(r.pending))
	}
}
