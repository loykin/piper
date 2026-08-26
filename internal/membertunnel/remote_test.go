package membertunnel

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

// deliverHTTP must never block the shared tunnel recv loop, even when the
// stream's own consumer hasn't caught up yet — but unlike the old
// bounded-channel design, an unconsumed backlog must queue and eventually be
// delivered in order, not get silently dropped with the stream torn down
// (that previously corrupted any large-enough upload/download once its
// buffer filled, which is exactly what this queue exists to carry).
func TestRemoteHTTPBackpressureDoesNotBlockOrDropFrames(t *testing.T) {
	r := newRemoteMemberClient("member-1", "test-token", func(*agentpb.HomeMessage) error { return nil })
	q := newHTTPFrameQueue()
	q.push(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("first")})
	r.streams = map[string]*httpFrameQueue{"slow": q}

	done := make(chan struct{})
	go func() {
		r.deliverHTTP(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("overflow")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delivering to an undrained stream blocked the tunnel receive loop")
	}

	r.mu.Lock()
	_, retained := r.streams["slow"]
	r.mu.Unlock()
	if !retained {
		t.Fatal("stream was torn down instead of just queuing the extra frame")
	}

	ctx := context.Background()
	first, ok := q.pop(ctx)
	if !ok || string(first.Data) != "first" {
		t.Fatalf("first popped frame = %+v ok=%v, want data=%q", first, ok, "first")
	}
	second, ok := q.pop(ctx)
	if !ok || string(second.Data) != "overflow" {
		t.Fatalf("second popped frame = %+v ok=%v, want data=%q", second, ok, "overflow")
	}
}

func TestMemberHTTPBackpressureDoesNotBlockOrDropFrames(t *testing.T) {
	c := NewClient(Config{}, &fakeMember{})
	q := newHTTPFrameQueue()
	q.push(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("first")})
	c.httpIn["slow"] = q

	done := make(chan struct{})
	go func() {
		c.deliverHTTP(&agentpb.MemberHTTPStreamData{StreamId: "slow", Data: []byte("overflow")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delivering to an undrained stream blocked the tunnel receive loop")
	}

	c.httpMu.Lock()
	_, retained := c.httpIn["slow"]
	c.httpMu.Unlock()
	if !retained {
		t.Fatal("stream was torn down instead of just queuing the extra frame")
	}

	ctx := context.Background()
	first, ok := q.pop(ctx)
	if !ok || string(first.Data) != "first" {
		t.Fatalf("first popped frame = %+v ok=%v, want data=%q", first, ok, "first")
	}
	second, ok := q.pop(ctx)
	if !ok || string(second.Data) != "overflow" {
		t.Fatalf("second popped frame = %+v ok=%v, want data=%q", second, ok, "overflow")
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
