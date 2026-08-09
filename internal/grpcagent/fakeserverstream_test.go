package grpcagent

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc/metadata"

	"github.com/loykin/piper/internal/agentpb"
)

// fakeServerStream is a minimal agentpb.AgentService_ConnectServer for
// testing workerConn behavior (proxy overflow, sendRPC, etc.) without a real
// network listener. Recv() returns io.EOF immediately unless messages have
// been queued via push — tests that only need Send-side behavior (the
// majority) never call push, so Recv's default EOF-on-first-call preserves
// their existing behavior unchanged.
type fakeServerStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	sent   []*agentpb.MasterMessage
	inbox  []*agentpb.WorkerMessage
	closed bool
}

func newFakeServerStream() *fakeServerStream {
	s := &fakeServerStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *fakeServerStream) Send(msg *agentpb.MasterMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	if s.cond != nil {
		s.cond.Broadcast()
	}
	return nil
}

func (s *fakeServerStream) Recv() (*agentpb.WorkerMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cond == nil {
		return nil, io.EOF
	}
	for len(s.inbox) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.inbox) == 0 {
		return nil, io.EOF
	}
	msg := s.inbox[0]
	s.inbox = s.inbox[1:]
	return msg, nil
}

func (s *fakeServerStream) Context() context.Context     { return context.Background() }
func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}
func (s *fakeServerStream) SendMsg(_ any) error          { return nil }
func (s *fakeServerStream) RecvMsg(_ any) error          { return io.EOF }

func (s *fakeServerStream) sentMessages() []*agentpb.MasterMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*agentpb.MasterMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

// push queues an inbound WorkerMessage for a Recv() call to return. Only
// usable on a stream created via newFakeServerStream (the plain zero-value
// fakeServerStream{} used by most tests has a nil cond and stays EOF-only).
func (s *fakeServerStream) push(msg *agentpb.WorkerMessage) {
	s.mu.Lock()
	s.inbox = append(s.inbox, msg)
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *fakeServerStream) close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// waitForSentCount blocks until at least n messages have been sent, or
// returns false if the stream closes first without reaching n.
func (s *fakeServerStream) waitForSentCount(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.sent) < n && !s.closed {
		s.cond.Wait()
	}
	return len(s.sent) >= n
}
