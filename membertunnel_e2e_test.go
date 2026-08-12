//go:build e2e

package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/membertunnel"
	"github.com/loykin/piper/internal/testutil"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// TestMemberTunnelSubmitRunEndToEnd proves fed.md §13.4's minimal slice:
// two independent *Piper processes (a Home and a remote Member, each with
// its own DB) wired over a real loopback gRPC tunnel. A run submitted
// through Home's real HTTP API executes on the Member's own in-process
// baremetal runtime (fed.md §13.2) and the result flows back through the
// tunnel to Home's HTTP API — without Home ever touching the Member's
// execution repository directly.
//
// serve.go/production routing is untouched by this test: Home's real
// server still wires NewLocalMemberClient (see memberclient_local.go).
// Here we build a small standalone router for just the /runs domain, wired
// to membertunnel's RemoteMemberClient instead, specifically to prove the
// tunnel path in isolation — see fed.md §13.4's plan for why wiring this
// into serve.go is deferred until a real Project-to-Member routing table
// exists.
func TestMemberTunnelSubmitRunEndToEnd(t *testing.T) {
	const projectID = "proj-1"

	// Member: owns real execution (queue, repos, in-process baremetal
	// runtime backend from fed.md §13.2) but exposes no HTTP/UI (fed.md §10.7).
	memberP, err := New(Config{
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "member.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("new member piper: %v", err)
	}
	t.Cleanup(func() { _ = memberP.Close() })
	if err := memberP.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatalf("create project on member: %v", err)
	}

	// Home: owns the tunnel server + the real user-facing HTTP API.
	homeP, err := New(Config{
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "home.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("new home piper: %v", err)
	}
	t.Cleanup(func() { _ = homeP.Close() })
	if err := homeP.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatalf("create project on home: %v", err)
	}

	// Wire the tunnel: Home runs a gRPC MemberTunnelService server, Member
	// dials out to it and enrolls.
	tunnelSrv := membertunnel.NewServer(membertunnel.ServerConfig{
		HomeID: "home-1",
		Tokens: map[string]string{"member-1": "secret"},
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	agentpb.RegisterMemberTunnelServiceServer(grpcServer, tunnelSrv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	tunnelCtx, tunnelCancel := context.WithCancel(context.Background())
	t.Cleanup(tunnelCancel)
	tunnelCli := membertunnel.NewClient(membertunnel.Config{
		HomeURL:  "http://" + lis.Addr().String(),
		HomeID:   "home-1",
		MemberID: "member-1",
		Token:    "secret",
	}, NewLocalMemberClient(memberP))
	go func() { _ = tunnelCli.Run(tunnelCtx) }()

	var remoteMember memberclient.Client
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := tunnelSrv.Client("member-1"); ok {
			remoteMember = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if remoteMember == nil {
		t.Fatal("member never enrolled with home tunnel")
	}

	// A minimal Home-side router for just /runs, wired to the RemoteMemberClient.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	runHandler := run.NewHandler(run.HandlerDeps{
		Member:     remoteMember,
		ProjectRef: project.LocalRef,
	})
	runHandler.RegisterRoutes(router.Group("/projects/:project_id",
		project.Require(homeP.repos.Project, nil, security.ProjectRoleViewer)))
	homeSrv := testutil.NewIPv4Server(t, router)

	// Submit a run through Home's real HTTP API.
	yaml := `
metadata:
  name: member-tunnel-e2e
spec:
  steps:
    - name: hello
      run:
        command: ["echo", "hello-from-member-tunnel"]
`
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	resp, err := http.Post(homeSrv.URL+"/projects/"+projectID+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /runs status = %d: %s", resp.StatusCode, b)
	}
	var submitResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		t.Fatal(err)
	}
	if submitResp.RunID == "" {
		t.Fatal("empty run_id")
	}

	// Poll Home's HTTP API — routed through the tunnel to the Member — until
	// the run completes on the Member side.
	var status string
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(homeSrv.URL + "/projects/" + projectID + "/runs/" + submitResp.RunID)
		if err == nil {
			var got struct {
				Run struct {
					Status string `json:"status"`
				} `json:"run"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&got)
			resp.Body.Close()
			status = got.Run.Status
			if status == run.StatusSuccess || status == run.StatusFailed {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status != run.StatusSuccess {
		t.Fatalf("run status via home = %q, want success", status)
	}

	// Confirm the run actually executed on the Member's own repos — Home
	// never wrote to them directly, only through the tunnel.
	memberRun, err := memberP.repos.Run.Get(context.Background(), projectID, submitResp.RunID)
	if err != nil || memberRun == nil {
		t.Fatalf("run not found in member's own repository: %v", err)
	}
	if memberRun.Status != run.StatusSuccess {
		t.Fatalf("member-side run status = %q, want success", memberRun.Status)
	}

	// And Home's own repos were never touched for this run's execution state.
	if homeRun, _ := homeP.repos.Run.Get(context.Background(), projectID, submitResp.RunID); homeRun != nil {
		t.Fatal("run leaked into home's own repository; Home must only reach it through the tunnel")
	}
}

// TestMemberTunnelCancelRunEndToEnd proves a second Run-domain mutation
// (not just submit) round-trips through the tunnel correctly.
func TestMemberTunnelCancelRunEndToEnd(t *testing.T) {
	const projectID = "proj-1"

	memberP, err := New(Config{
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "member.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("new member piper: %v", err)
	}
	t.Cleanup(func() { _ = memberP.Close() })
	if err := memberP.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	homeP, err := New(Config{
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "home.db"),
		Auth:      AuthConfig{Trusted: true},
		Server:    ServerConfig{AllowInsecureDevKey: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("new home piper: %v", err)
	}
	t.Cleanup(func() { _ = homeP.Close() })
	if err := homeP.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	tunnelSrv := membertunnel.NewServer(membertunnel.ServerConfig{
		HomeID: "home-1",
		Tokens: map[string]string{"member-1": "secret"},
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	agentpb.RegisterMemberTunnelServiceServer(grpcServer, tunnelSrv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	tunnelCtx, tunnelCancel := context.WithCancel(context.Background())
	t.Cleanup(tunnelCancel)
	tunnelCli := membertunnel.NewClient(membertunnel.Config{
		HomeURL:  "http://" + lis.Addr().String(),
		HomeID:   "home-1",
		MemberID: "member-1",
		Token:    "secret",
	}, NewLocalMemberClient(memberP))
	go func() { _ = tunnelCli.Run(tunnelCtx) }()

	var remoteMember memberclient.Client
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := tunnelSrv.Client("member-1"); ok {
			remoteMember = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if remoteMember == nil {
		t.Fatal("member never enrolled with home tunnel")
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	run.NewHandler(run.HandlerDeps{
		Member:     remoteMember,
		ProjectRef: project.LocalRef,
	}).RegisterRoutes(router.Group("/projects/:project_id",
		project.Require(homeP.repos.Project, nil, security.ProjectRoleViewer)))
	homeSrv := testutil.NewIPv4Server(t, router)

	yaml := `
metadata:
  name: member-tunnel-cancel
spec:
  steps:
    - name: slow
      run:
        command: ["sleep", "60"]
`
	body, _ := json.Marshal(map[string]any{"yaml": yaml})
	resp, err := http.Post(homeSrv.URL+"/projects/"+projectID+"/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var submitResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		t.Fatal(err)
	}

	// Wait until the member actually started the step before cancelling.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := memberP.repos.Run.Get(context.Background(), projectID, submitResp.RunID)
		if r != nil && r.Status == run.StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancelResp, err := http.Post(homeSrv.URL+"/projects/"+projectID+"/runs/"+submitResp.RunID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cancelResp.Body)
		t.Fatalf("POST /cancel status = %d: %s", cancelResp.StatusCode, b)
	}

	deadline = time.Now().Add(10 * time.Second)
	var memberRun *run.Run
	for time.Now().Before(deadline) {
		memberRun, _ = memberP.repos.Run.Get(context.Background(), projectID, submitResp.RunID)
		if memberRun != nil && memberRun.Status == run.StatusCanceled {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if memberRun == nil || memberRun.Status != run.StatusCanceled {
		got := "<nil>"
		if memberRun != nil {
			got = memberRun.Status
		}
		t.Fatalf("member-side run status = %q, want canceled", got)
	}
}
