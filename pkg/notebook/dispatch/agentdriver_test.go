package notebookdispatch

import (
	"context"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/notebook"
)

type recordingAgentRPC struct {
	calls []agentRPCCall
}

type agentRPCCall struct {
	AgentID string
	Method  string
	Payload any
}

func (r *recordingAgentRPC) SendRPC(_ context.Context, agentID, method string, payload any, result any) error {
	r.calls = append(r.calls, agentRPCCall{AgentID: agentID, Method: method, Payload: payload})
	switch method {
	case iagent.MethodNotebookProvisionVolume:
		res := result.(*notebook.WorkerProvisionVolumeResponse)
		res.WorkDir = notebook.ContainerWorkDir
	case iagent.MethodNotebookStart:
		res := result.(*notebook.WorkerStartResponse)
		res.Token = "token"
		res.WorkDir = notebook.ContainerWorkDir
		res.Endpoint = "tunnel://agent-1/nb/demo"
	case iagent.MethodNotebookSyncStatus:
		res := result.(*notebook.WorkerSyncStatusResponse)
		res.Statuses = map[string]string{"project-a:demo": notebook.StatusRunning}
	}
	return nil
}

func newAgentNotebookDriver() (*AgentDriver, *recordingAgentRPC) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:             "agent-1",
		Infrastructure: iagent.InfrastructureK8s,
		ClusterName:    "gpu-a",
		Capabilities:   []string{iagent.CapabilityNotebook},
	})
	rpc := &recordingAgentRPC{}
	return NewAgentDriver(iagent.NewRouter(reg), rpc, nil), rpc
}

func TestAgentDriverProvisionVolume(t *testing.T) {
	driver, rpc := newAgentNotebookDriver()
	vol := &notebook.NotebookVolume{ID: "vol-1"}

	spec := notebook.Notebook{Spec: notebook.NotebookSpec{Volume: &notebook.VolumeSpec{Size: "5Gi"}}}
	if err := driver.ProvisionVolume(context.Background(), vol, spec); err != nil {
		t.Fatalf("ProvisionVolume returned error: %v", err)
	}
	if vol.WorkDir != notebook.ContainerWorkDir {
		t.Fatalf("work dir = %q", vol.WorkDir)
	}
	if vol.WorkerID != "agent-1" {
		t.Fatalf("worker id = %q", vol.WorkerID)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookProvisionVolume {
		t.Fatalf("method = %q", rpc.calls[0].Method)
	}
}

func TestAgentDriverStartUsesVolumeAgent(t *testing.T) {
	driver, rpc := newAgentNotebookDriver()
	spec := notebook.Notebook{}
	spec.Metadata.Name = "demo"

	nb, err := driver.Start(context.Background(), spec, &notebook.NotebookVolume{ProjectID: "project-a", ID: "vol-1", WorkerID: "agent-1", WorkDir: notebook.ContainerWorkDir}, "metadata:\n  name: demo\n")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if nb.Endpoint != "tunnel://agent-1/nb/demo" {
		t.Fatalf("endpoint = %q", nb.Endpoint)
	}
	if nb.WorkerID != "agent-1" {
		t.Fatalf("worker id = %q", nb.WorkerID)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookStart {
		t.Fatalf("method = %q", rpc.calls[0].Method)
	}
	payload, ok := rpc.calls[0].Payload.(notebook.WorkerStartRequest)
	if !ok || payload.ProjectID != "project-a" {
		t.Fatalf("start payload = %#v", rpc.calls[0].Payload)
	}
	if nb.ProjectID != "project-a" {
		t.Fatalf("project id = %q", nb.ProjectID)
	}
}

func TestAgentDriverStartFallsBackFromStaleVolumeAgent(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-2", Infrastructure: iagent.InfrastructureBaremetal, Capabilities: []string{iagent.CapabilityNotebook}})
	rpc := &recordingAgentRPC{}
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, nil)
	spec := notebook.Notebook{}
	spec.Metadata.Name = "demo"
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkerID: "old-agent", WorkDir: "/work/vol-1"}

	nb, err := driver.Start(context.Background(), spec, vol, "metadata:\n  name: demo\n")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if nb.WorkerID != "agent-2" {
		t.Fatalf("worker id = %q, want agent-2", nb.WorkerID)
	}
	if vol.WorkerID != "agent-2" {
		t.Fatalf("volume worker id = %q, want agent-2", vol.WorkerID)
	}
	if rpc.calls[0].AgentID != "agent-2" {
		t.Fatalf("rpc agent = %q, want agent-2", rpc.calls[0].AgentID)
	}
}

func TestAgentDriverStartDoesNotFallbackForExplicitPlacement(t *testing.T) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-2", Infrastructure: iagent.InfrastructureBaremetal, Capabilities: []string{iagent.CapabilityNotebook}})
	driver := NewAgentDriver(iagent.NewRouter(reg), &recordingAgentRPC{}, nil)
	spec := notebook.Notebook{}
	spec.Metadata.Name = "demo"
	spec.Spec.Driver.Placement = manifest.PlacementSpec{Worker: "old-agent"}

	if _, err := driver.Start(context.Background(), spec, &notebook.NotebookVolume{ID: "vol-1", WorkDir: "/work/vol-1"}, "metadata:\n  name: demo\n"); err == nil {
		t.Fatal("expected explicit stale placement to fail")
	}
}

func TestAgentDriverStopAndDeprovision(t *testing.T) {
	driver, rpc := newAgentNotebookDriver()

	if err := driver.Stop(context.Background(), &notebook.NotebookServer{ProjectID: "project-a", Name: "demo", WorkerID: "agent-1"}); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := driver.DeprovisionVolume(context.Background(), &notebook.NotebookVolume{ID: "vol-1", WorkerID: "agent-1"}); err != nil {
		t.Fatalf("DeprovisionVolume returned error: %v", err)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookStop {
		t.Fatalf("first method = %q", rpc.calls[0].Method)
	}
	payload, ok := rpc.calls[0].Payload.(notebook.WorkerStopRequest)
	if !ok || payload.ProjectID != "project-a" || payload.Name != "demo" {
		t.Fatalf("stop payload = %#v", rpc.calls[0].Payload)
	}
	if rpc.calls[1].Method != iagent.MethodNotebookDeprovision {
		t.Fatalf("second method = %q", rpc.calls[1].Method)
	}
}

func TestAgentDriverSyncStatusSkipsDisconnectedAgent(t *testing.T) {
	// When the agent is offline, SyncStatus must NOT change the notebook status.
	// The worker is the source of truth; an unreachable agent means "unknown", not "stopped".
	repo := newFakeRepo()
	if err := repo.Create(context.Background(), &notebook.NotebookServer{Name: "demo", WorkerID: "old-agent", Status: notebook.StatusRunning}); err != nil {
		t.Fatalf("create repo record: %v", err)
	}
	reg := iagent.NewRegistry()
	driver := NewAgentDriver(iagent.NewRouter(reg), &recordingAgentRPC{}, repo)

	applied := false
	if err := driver.SyncStatus(context.Background(), []*notebook.NotebookServer{{Name: "demo", WorkerID: "old-agent", Status: notebook.StatusRunning}}, func(_, name, status string) {
		applied = true
		_ = repo.SetStatus(context.Background(), "", name, status)
	}); err != nil {
		t.Fatalf("SyncStatus returned error: %v", err)
	}
	if applied {
		t.Fatal("apply was called for offline agent; expected no-op")
	}
	nb, _ := repo.Get(context.Background(), "", "demo")
	if nb.Status != notebook.StatusRunning {
		t.Fatalf("status changed to %q; want unchanged %q", nb.Status, notebook.StatusRunning)
	}
}

func TestAgentDriverSyncStatus(t *testing.T) {
	repo := newFakeRepo()
	if err := repo.Create(context.Background(), &notebook.NotebookServer{ProjectID: "project-a", Name: "demo", WorkerID: "agent-1", Status: notebook.StatusStarting}); err != nil {
		t.Fatalf("create repo record: %v", err)
	}
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Infrastructure: iagent.InfrastructureK8s, Capabilities: []string{iagent.CapabilityNotebook}})
	rpc := &recordingAgentRPC{}
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, repo)

	if err := driver.SyncStatus(context.Background(), []*notebook.NotebookServer{{
		ProjectID: "project-a",
		Name:      "demo",
		WorkerID:  "agent-1",
		Endpoint:  "tunnel://agent-1?target=127.0.0.1:18888",
	}}, func(projectID, name, status string) {
		_ = repo.SetStatus(context.Background(), projectID, name, status)
	}); err != nil {
		t.Fatalf("SyncStatus returned error: %v", err)
	}
	nb, _ := repo.Get(context.Background(), "project-a", "demo")
	if nb.Status != notebook.StatusRunning {
		t.Fatalf("status = %q, want running", nb.Status)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookSyncStatus {
		t.Fatalf("method = %q", rpc.calls[0].Method)
	}
	req := rpc.calls[0].Payload.(notebook.WorkerSyncStatusRequest)
	if len(req.Targets) != 1 || req.Targets[0].Name != "demo" || req.Targets[0].Port != 18888 {
		t.Fatalf("sync targets = %+v", req.Targets)
	}
}

func TestAgentDriverStartNoAgent(t *testing.T) {
	// When no notebook agent is available, Start should return an error.
	reg := iagent.NewRegistry()
	driver := NewAgentDriver(iagent.NewRouter(reg), &recordingAgentRPC{}, nil)
	spec := notebook.Notebook{}
	spec.Metadata.Name = "demo"
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkerID: "nonexistent"}

	if _, err := driver.Start(context.Background(), spec, vol, "metadata:\n  name: demo\n"); err == nil {
		t.Fatal("expected error when no notebook agent is registered")
	}
}
