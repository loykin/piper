package notebookdispatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	iagent "github.com/piper/piper/internal/agent"
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
		res.Statuses = map[string]string{"demo": notebook.StatusRunning}
	}
	return nil
}

func newAgentNotebookDriver() (*AgentDriver, *recordingAgentRPC) {
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "agent-1",
		Kind:         iagent.KindK8s,
		ClusterName:  "gpu-a",
		Capabilities: []string{iagent.CapabilityNotebook},
	})
	rpc := &recordingAgentRPC{}
	return NewAgentDriver(iagent.NewRouter(reg), rpc), rpc
}

func TestAgentDriverProvisionVolume(t *testing.T) {
	driver, rpc := newAgentNotebookDriver()
	vol := &notebook.NotebookVolume{ID: "vol-1"}

	if err := driver.ProvisionVolume(context.Background(), vol, "5Gi"); err != nil {
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
	spec := notebook.NotebookServerSpec{}
	spec.Metadata.Name = "demo"

	nb, err := driver.Start(context.Background(), spec, &notebook.NotebookVolume{ID: "vol-1", WorkerID: "agent-1", WorkDir: notebook.ContainerWorkDir}, "metadata:\n  name: demo\n")
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
}

func TestAgentDriverStopAndDeprovision(t *testing.T) {
	driver, rpc := newAgentNotebookDriver()

	if err := driver.Stop(context.Background(), &notebook.NotebookServer{Name: "demo", WorkerID: "agent-1"}); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := driver.DeprovisionVolume(context.Background(), &notebook.NotebookVolume{ID: "vol-1", WorkerID: "agent-1"}); err != nil {
		t.Fatalf("DeprovisionVolume returned error: %v", err)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookStop {
		t.Fatalf("first method = %q", rpc.calls[0].Method)
	}
	if rpc.calls[1].Method != iagent.MethodNotebookDeprovision {
		t.Fatalf("second method = %q", rpc.calls[1].Method)
	}
}

func TestAgentDriverSyncStatus(t *testing.T) {
	repo := newFakeRepo()
	if err := repo.Create(context.Background(), &notebook.NotebookServer{Name: "demo", WorkerID: "agent-1", Status: notebook.StatusStarting}); err != nil {
		t.Fatalf("create repo record: %v", err)
	}
	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{ID: "agent-1", Kind: iagent.KindK8s, Capabilities: []string{iagent.CapabilityNotebook}})
	rpc := &recordingAgentRPC{}
	driver := NewAgentDriver(iagent.NewRouter(reg), rpc, repo)

	if err := driver.SyncStatus(context.Background(), []*notebook.NotebookServer{{Name: "demo", WorkerID: "agent-1"}}); err != nil {
		t.Fatalf("SyncStatus returned error: %v", err)
	}
	nb, _ := repo.Get(context.Background(), "demo")
	if nb.Status != notebook.StatusRunning {
		t.Fatalf("status = %q, want running", nb.Status)
	}
	if rpc.calls[0].Method != iagent.MethodNotebookSyncStatus {
		t.Fatalf("method = %q", rpc.calls[0].Method)
	}
}

func TestAgentDriverBareMetalUsesDirectWorkerHTTP(t *testing.T) {
	var sawVolume bool
	var sawStart bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/volume":
			sawVolume = true
			_, _ = w.Write([]byte(`{"work_dir":"/tmp/vol-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/start":
			sawStart = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"token":"token","work_dir":"/tmp/vol-1"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer worker.Close()

	reg := iagent.NewRegistry()
	reg.Register(iagent.Info{
		ID:           "worker-1",
		Kind:         iagent.KindBareMetal,
		Addr:         worker.URL,
		Capabilities: []string{iagent.CapabilityNotebook},
	})
	driver := NewAgentDriver(iagent.NewRouter(reg), &recordingAgentRPC{})
	vol := &notebook.NotebookVolume{ID: "vol-1"}

	if err := driver.ProvisionVolume(context.Background(), vol, ""); err != nil {
		t.Fatalf("ProvisionVolume returned error: %v", err)
	}
	spec := notebook.NotebookServerSpec{}
	spec.Metadata.Name = "demo"
	nb, err := driver.Start(context.Background(), spec, vol, "metadata:\n  name: demo\n")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !sawVolume || !sawStart {
		t.Fatalf("worker requests volume=%v start=%v", sawVolume, sawStart)
	}
	if nb.WorkerID != "worker-1" {
		t.Fatalf("worker id = %q", nb.WorkerID)
	}
	if vol.WorkerID != "worker-1" {
		t.Fatalf("volume worker id = %q", vol.WorkerID)
	}
}
