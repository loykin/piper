package membertunnel

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
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
	states := make(chan bool, 2)
	srv, addr := startTestServer(t, ServerConfig{
		HomeID: "home-1",
		Tokens: map[string]string{"member-1": "secret"},
		OnConnectionChanged: func(_ context.Context, homeID, memberID string, connected bool) error {
			if homeID != "home-1" || memberID != "member-1" {
				t.Errorf("connection callback identity = %q/%q", homeID, memberID)
			}
			states <- connected
			return nil
		},
	})

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

	resp, err := mc.SubmitRun(ctx, memberclient.AuthContext{}, project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "proj-1"}, memberclient.SubmitRunRequest{YAML: "x", Experiment: "exp-1"})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if resp.RunID != "run-proj-1-exp-1" {
		t.Fatalf("RunID = %q, want run-proj-1-exp-1", resp.RunID)
	}
	if connected := <-states; !connected {
		t.Fatal("first lifecycle callback was not connected")
	}
	cancel()
	select {
	case connected := <-states:
		if connected {
			t.Fatal("disconnect lifecycle callback reported connected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect lifecycle callback was not called")
	}
}

func TestTunnelEndToEndProjectRequest(t *testing.T) {
	srv, addr := startTestServer(t, ServerConfig{
		HomeID: "home-1",
		Tokens: map[string]string{"member-1": "secret"},
	})
	projectMember := &fakeProjectClient{doFn: func(_ context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req projectclient.Request) (projectclient.Response, error) {
		if auth.ActorID != "user-1" || auth.Role != security.ProjectRoleMember {
			t.Fatalf("delegated auth = %+v", auth)
		}
		if ref.ProjectID != "project-1" || req.Path != "/pipelines" || req.Method != "GET" {
			t.Fatalf("ref=%+v request=%+v", ref, req)
		}
		return projectclient.Response{Status: 200, Body: []byte(`[{"name":"remote"}]`)}, nil
	}}
	cli := NewClient(
		Config{HomeURL: "http://" + addr, HomeID: "home-1", MemberID: "member-1", Token: "secret"},
		&fakeMember{}, projectMember,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = cli.Run(ctx) }()

	var remote projectclient.Client
	if !waitUntilTunnel(2*time.Second, func() bool {
		member, ok := srv.Client("member-1")
		if !ok {
			return false
		}
		remote, ok = member.(projectclient.Client)
		return ok
	}) {
		t.Fatal("project client never became available")
	}
	response, err := remote.DoProjectRequest(ctx,
		memberclient.AuthContext{ActorID: "user-1", Role: security.ProjectRoleMember},
		project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"},
		projectclient.Request{Method: "GET", Path: "/pipelines"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 200 || string(response.Body) != `[{"name":"remote"}]` {
		t.Fatalf("response = %+v", response)
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
