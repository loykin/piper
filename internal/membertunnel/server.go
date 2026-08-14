package membertunnel

import (
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
)

// ServerConfig configures the Home-side tunnel server.
type ServerConfig struct {
	HomeID string
	// Tokens maps MemberID to its static enrollment secret. One token per
	// Member (not a single shared secret like the worker tunnel's
	// WorkerToken) — see the package doc for why rotation/revocation isn't
	// built yet.
	Tokens map[string]string
}

// Server is the Home-side MemberTunnelService implementation: it accepts
// enrollment from remote Members and exposes a memberclient.Client per
// enrolled Member via Client.
type Server struct {
	agentpb.UnimplementedMemberTunnelServiceServer
	cfg ServerConfig

	mu      sync.RWMutex
	members map[string]*remoteMemberClient // memberID → active connection
}

// NewServer creates a Home-side tunnel server. Register it with
// agentpb.RegisterMemberTunnelServiceServer(grpcServer, srv).
func NewServer(cfg ServerConfig) *Server {
	return &Server{cfg: cfg, members: make(map[string]*remoteMemberClient)}
}

// Client returns the memberclient.Client for an enrolled, currently
// connected Member.
func (s *Server) Client(memberID string) (memberclient.Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.members[memberID]
	return c, ok
}

// Connect implements agentpb.MemberTunnelServiceServer. The first frame
// must be a MemberEnrollment with a token matching cfg.Tokens[member_id];
// every subsequent frame must be a MemberRPCResponse correlated to a
// pending call made through the resulting remoteMemberClient.
func (s *Server) Connect(stream agentpb.MemberTunnelService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	enroll := first.GetEnroll()
	if enroll == nil {
		return status.Error(codes.InvalidArgument, "membertunnel: first frame must be MemberEnrollment")
	}
	if enroll.HomeId != s.cfg.HomeID {
		return status.Errorf(codes.PermissionDenied, "membertunnel: home_id %q does not match this Home", enroll.HomeId)
	}
	wantToken, known := s.cfg.Tokens[enroll.MemberId]
	if !known || wantToken == "" || wantToken != enroll.Token {
		return status.Errorf(codes.PermissionDenied, "membertunnel: member %q failed enrollment", enroll.MemberId)
	}

	var sendMu sync.Mutex
	send := func(msg *agentpb.HomeMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}
	rc := newRemoteMemberClient(enroll.MemberId, wantToken, send)

	s.mu.Lock()
	s.members[enroll.MemberId] = rc
	s.mu.Unlock()
	slog.Info("member enrolled", "member_id", enroll.MemberId, "home_id", enroll.HomeId)
	defer func() {
		s.mu.Lock()
		if s.members[enroll.MemberId] == rc {
			delete(s.members, enroll.MemberId)
		}
		s.mu.Unlock()
		rc.closeAll()
		slog.Info("member disconnected", "member_id", enroll.MemberId)
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		resp := msg.GetResponse()
		if resp == nil {
			return fmt.Errorf("membertunnel: member %q sent an unexpected frame after enrollment", enroll.MemberId)
		}
		rc.deliver(resp)
	}
}

var _ agentpb.MemberTunnelServiceServer = (*Server)(nil)
