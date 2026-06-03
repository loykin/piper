package notebookworker

import (
	"context"
	"testing"
	"time"

	"github.com/piper/piper/pkg/notebook"
)

type conformanceRuntime struct {
	req     RuntimeStartRequest
	started chan struct{}
}

func (r *conformanceRuntime) Start(_ context.Context, req RuntimeStartRequest) (*StartedNotebook, error) {
	r.req = req
	if r.started != nil {
		close(r.started)
	}
	return &StartedNotebook{Endpoint: "http://localhost:18888", PID: 123}, nil
}

func (r *conformanceRuntime) Stop(context.Context, string) error { return nil }
func (r *conformanceRuntime) KillAll(context.Context) error      { return nil }
func (r *conformanceRuntime) Status(name string) string {
	if name == "running-nb" {
		return notebook.StatusRunning
	}
	return notebook.StatusStopped
}

func TestNotebookWorker_StartInvalidYAML(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		YAML: ":::not yaml:::",
	})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestNotebookWorker_StartMissingName(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: ""
spec:
  runtime:
    port: 8888
`
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		YAML: yamlPayload,
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNotebookWorker_StartMissingVolumeID(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	yamlPayload := `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: test-nb
spec:
  runtime:
    port: 8888
`
	_, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		YAML:     yamlPayload,
		VolumeID: "",
		WorkDir:  "",
	})
	if err == nil {
		t.Fatal("expected error for missing volume_id and work_dir")
	}
}

func TestNotebookWorkerStartResponseConformance(t *testing.T) {
	rt := &conformanceRuntime{started: make(chan struct{})}
	workDir := t.TempDir()
	w := New(Config{ID: "agent-1", PortRange: "18888-18888"})
	w.runtime = rt
	w.portAllocator = func() (int, error) { return 18888, nil }

	resp, err := w.startNotebook(context.Background(), notebook.WorkerStartRequest{
		YAML:     "metadata:\n  name: demo\nspec: {}\n",
		VolumeID: "vol-demo",
		WorkDir:  workDir,
	})
	if err != nil {
		t.Fatalf("startNotebook returned error: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("token is empty")
	}
	if resp.WorkDir != workDir {
		t.Fatalf("work dir = %q", resp.WorkDir)
	}
	if resp.Endpoint != "tunnel://agent-1?target=127.0.0.1:18888" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("runtime was not started")
	}
	if rt.req.BaseURL != "/notebooks/demo/proxy/" {
		t.Fatalf("base url = %q", rt.req.BaseURL)
	}
	if rt.req.Token == "" {
		t.Fatal("runtime token is empty; JupyterLab requires a real token")
	}
}

func TestNotebookWorker_StopNonExistent(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	// stopNotebook is idempotent for non-existent notebooks.
	if err := w.stopNotebook(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("stopNotebook nonexistent: %v", err)
	}
}

func TestNotebookWorker_ProvisionVolumeEmptyID(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	_, err := w.provisionVolume(context.Background(), notebook.WorkerProvisionVolumeRequest{VolumeID: ""})
	if err == nil {
		t.Fatal("expected error for empty volume_id")
	}
}

func TestNotebookWorker_SyncStatus(t *testing.T) {
	w := New(Config{ID: "nb-test-id"})
	w.runtime = &conformanceRuntime{}

	resp, err := w.syncStatus(context.Background(), notebook.WorkerSyncStatusRequest{
		Names: []string{"running-nb", "stopped-nb"},
	})
	if err != nil {
		t.Fatalf("syncStatus: %v", err)
	}
	if resp.Statuses["running-nb"] != notebook.StatusRunning {
		t.Errorf("running-nb status = %q, want %q", resp.Statuses["running-nb"], notebook.StatusRunning)
	}
	if resp.Statuses["stopped-nb"] != notebook.StatusStopped {
		t.Errorf("stopped-nb status = %q, want %q", resp.Statuses["stopped-nb"], notebook.StatusStopped)
	}
}

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
		start   int
		end     int
	}{
		{"8888-9900", false, 8888, 9900},
		{"1000-1000", false, 1000, 1000},
		{"bad", true, 0, 0},
		{"9000-8000", true, 0, 0},
	}
	for _, tc := range cases {
		start, end, err := parsePortRange(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parsePortRange(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePortRange(%q): %v", tc.input, err)
			continue
		}
		if start != tc.start || end != tc.end {
			t.Errorf("parsePortRange(%q) = (%d,%d), want (%d,%d)", tc.input, start, end, tc.start, tc.end)
		}
	}
}
