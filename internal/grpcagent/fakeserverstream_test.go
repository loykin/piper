package grpcagent

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc/metadata"

	"github.com/piper/piper/internal/agentpb"
)

// fakeServerStream is a minimal agentpb.AgentService_ConnectServer for
// testing workerConn behavior (proxy overflow, sendRPC, etc.) without a real
// network listener.
type fakeServerStream struct {
	mu   sync.Mutex
	sent []*agentpb.MasterMessage
}

func (s *fakeServerStream) Send(msg *agentpb.MasterMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

func (s *fakeServerStream) Recv() (*agentpb.WorkerMessage, error) { return nil, io.EOF }
func (s *fakeServerStream) Context() context.Context              { return context.Background() }
func (s *fakeServerStream) SetHeader(metadata.MD) error           { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error          { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)                {}
func (s *fakeServerStream) SendMsg(_ any) error                   { return nil }
func (s *fakeServerStream) RecvMsg(_ any) error                   { return io.EOF }

func (s *fakeServerStream) sentMessages() []*agentpb.MasterMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*agentpb.MasterMessage, len(s.sent))
	copy(out, s.sent)
	return out
}
