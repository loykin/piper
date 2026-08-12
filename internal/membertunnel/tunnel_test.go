package membertunnel

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

func startTestServer(t *testing.T, cfg ServerConfig) (*Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg)
	grpcServer := grpc.NewServer()
	agentpb.RegisterMemberTunnelServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)
	return srv, lis.Addr().String()
}

func waitUntilTunnel(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestTunnelEndToEndSubmitRun proves the full round trip: Member enrolls,
// Home's RemoteMemberClient (via Server.Client) sends a real RPC over a
// real loopback gRPC connection, the Member dispatches it to a local
// memberclient.Client, and the response decodes correctly back on the Home
// side.
func TestTunnelEndToEndSubmitRun(t *testing.T) {
	srv, addr := startTestServer(t, ServerConfig{HomeID: "home-1", Tokens: map[string]string{"member-1": "secret"}})

	member := &fakeMember{
		submitRunFn: func(_ context.Context, _ memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
			return memberclient.SubmitRunResponse{RunID: "run-" + ref.ProjectID + "-" + req.Experiment}, nil
		},
	}
	cli := NewClient(Config{HomeURL: "http://" + addr, HomeID: "home-1", MemberID: "member-1", Token: "secret"}, member)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = cli.Run(ctx) }()

	var mc memberclient.Client
	if !waitUntilTunnel(2*time.Second, func() bool {
		var ok bool
		mc, ok = srv.Client("member-1")
		return ok
	}) {
		t.Fatal("member never enrolled")
	}

	resp, err := mc.SubmitRun(ctx, memberclient.AuthContext{}, project.ProjectRef{ProjectID: "proj-1"}, memberclient.SubmitRunRequest{YAML: "x", Experiment: "exp-1"})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if resp.RunID != "run-proj-1-exp-1" {
		t.Fatalf("RunID = %q, want run-proj-1-exp-1", resp.RunID)
	}
}

// TestTunnelRejectsBadToken proves enrollment fails closed: a Member
// presenting the wrong token never gets registered.
func TestTunnelRejectsBadToken(t *testing.T) {
	srv, addr := startTestServer(t, ServerConfig{HomeID: "home-1", Tokens: map[string]string{"member-1": "secret"}})

	cli := NewClient(Config{HomeURL: "http://" + addr, HomeID: "home-1", MemberID: "member-1", Token: "wrong"}, &fakeMember{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cli.connectAndServe(ctx); err == nil {
		t.Fatal("expected enrollment to be rejected")
	}
	if _, ok := srv.Client("member-1"); ok {
		t.Fatal("member should not be registered after rejected enrollment")
	}
}

// TestTunnelRejectsWrongHomeID proves a Member enrolling against the wrong
// Home identity is rejected, not silently accepted.
func TestTunnelRejectsWrongHomeID(t *testing.T) {
	srv, addr := startTestServer(t, ServerConfig{HomeID: "home-1", Tokens: map[string]string{"member-1": "secret"}})

	cli := NewClient(Config{HomeURL: "http://" + addr, HomeID: "home-2", MemberID: "member-1", Token: "secret"}, &fakeMember{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cli.connectAndServe(ctx); err == nil {
		t.Fatal("expected enrollment to be rejected for mismatched home_id")
	}
	if _, ok := srv.Client("member-1"); ok {
		t.Fatal("member should not be registered after rejected enrollment")
	}
}
