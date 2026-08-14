package membertunnel

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

func TestCallRemovesPendingRequestOnContextCancellation(t *testing.T) {
	r := newRemoteMemberClient("member-1", func(*agentpb.HomeMessage) error { return nil })
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
