package grpcagent

import (
	"io"
	"sync"

	"github.com/loykin/piper/internal/agentpb"
)

// fakeWorkerStream is an in-memory workerStream for testing Client.serve
// without a real network listener or gRPC connection.
type fakeWorkerStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	inbox  []*agentpb.MasterMessage
	closed bool
	sent   []*agentpb.WorkerMessage
}

func newFakeWorkerStream() *fakeWorkerStream {
	s := &fakeWorkerStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *fakeWorkerStream) Send(msg *agentpb.WorkerMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.sent = append(s.sent, msg)
	s.cond.Broadcast()
	return nil
}

func (s *fakeWorkerStream) Recv() (*agentpb.MasterMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *fakeWorkerStream) push(msg *agentpb.MasterMessage) {
	s.mu.Lock()
	s.inbox = append(s.inbox, msg)
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *fakeWorkerStream) close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *fakeWorkerStream) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// sentAt returns the i-th sent message (0-indexed), or nil if out of range.
// Always go through this (or another locked accessor) rather than reading
// the sent field directly — Send appends to it from the serve goroutine
// concurrently with test assertions.
func (s *fakeWorkerStream) sentAt(i int) *agentpb.WorkerMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.sent) {
		return nil
	}
	return s.sent[i]
}

// waitForSentCount blocks (via the same cond used by Send) until at least n
// messages have been sent, or returns false if closed first without reaching n.
func (s *fakeWorkerStream) waitForSentCount(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.sent) < n && !s.closed {
		s.cond.Wait()
	}
	return len(s.sent) >= n
}
