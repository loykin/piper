package serving

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/piper/piper/pkg/store"
	_ "modernc.org/sqlite"
)

// openTestStore creates an in-memory SQLite store for testing.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ─── expandArgs ───────────────────────────────────────────────────────────────

func TestExpandArgs(t *testing.T) {
	env := map[string]string{
		"PIPER_MODEL_DIR":    "/models/v1",
		"PIPER_SERVICE_NAME": "my-svc",
	}
	input := []string{
		"tritonserver",
		"--model-repository=$(PIPER_MODEL_DIR)",
		"--http-port=8000",
		"--grpc-port=$(MISSING_VAR)",
	}
	got := expandArgs(input, env)

	if got[0] != "tritonserver" {
		t.Errorf("arg[0]: want tritonserver, got %q", got[0])
	}
	if got[1] != "--model-repository=/models/v1" {
		t.Errorf("arg[1]: want --model-repository=/models/v1, got %q", got[1])
	}
	if got[2] != "--http-port=8000" {
		t.Errorf("arg[2]: want unchanged, got %q", got[2])
	}
	// Missing env var: os.Expand returns "" for unknown keys not in env or OS
	_ = got[3] // just ensure no panic
}

func TestExpandArgs_empty(t *testing.T) {
	got := expandArgs(nil, nil)
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

// ─── ArtifactLocalPath ────────────────────────────────────────────────────────

func TestArtifactLocalPath(t *testing.T) {
	got := ArtifactLocalPath("/outputs", "run-123", "train", "model")
	want := filepath.Join("/outputs", "run-123", "train", "model")
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// ─── k8sName ─────────────────────────────────────────────────────────────────

func TestK8sName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"fraud-detector", "fraud-detector"},
		{"Fraud_Detector", "fraud-detector"},
		{"my service", "my-service"},
		{"", ""},
	}
	for _, tc := range tests {
		got := k8sName(tc.input)
		if got != tc.want {
			t.Errorf("k8sName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ─── Deploy local mode ────────────────────────────────────────────────────────

func TestDeployLocal_andStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping process test on windows")
	}

	// Start a real HTTP server to simulate the serving runtime endpoint.
	// The "runtime" in this test is: sh -c "while true; do sleep 0.1; done"
	// We won't health-check it (timeout will fire harmlessly), just verify PID is recorded.

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	st := openTestStore(t)
	dir := t.TempDir()
	mgr := New(st, dir, nil)

	// Extract port from test server URL for the fake runtime
	svc := ModelService{
		Metadata: Metadata{Name: "test-svc"},
		Spec: ModelServiceSpec{
			Model: ModelRef{
				FromArtifact: &ArtifactRef{
					Pipeline: "train",
					Step:     "train",
					Artifact: "model",
					Run:      "run-1",
				},
			},
			Runtime: RuntimeSpec{
				Image:   "unused-in-local-mode",
				Command: []string{"sleep", "60"},
				Port:    9999,
				Mode:    "local",
			},
		},
	}

	ctx := context.Background()
	artifactDir := filepath.Join(dir, "run-1", "train", "model")
	_ = os.MkdirAll(artifactDir, 0755)

	if err := mgr.Deploy(ctx, svc, artifactDir); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// Verify service was persisted
	rec, err := st.GetService("test-svc")
	if err != nil || rec == nil {
		t.Fatalf("GetService after Deploy: err=%v rec=%v", err, rec)
	}
	if rec.Status != store.ServiceStatusRunning {
		t.Errorf("status: want running, got %q", rec.Status)
	}
	if rec.PID == 0 {
		t.Error("PID should be non-zero after local deploy")
	}

	// Stop the service
	if err := mgr.Stop(ctx, "test-svc"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Allow the goroutine that watches the process to update status
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, _ = st.GetService("test-svc")
		if rec != nil && rec.Status != store.ServiceStatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	rec, _ = st.GetService("test-svc")
	if rec == nil {
		t.Fatal("service record missing after stop")
	}
	if rec.Status == store.ServiceStatusRunning {
		t.Errorf("service should not be running after Stop, got %q", rec.Status)
	}
}

func TestDeployLocal_missingCommand(t *testing.T) {
	st := openTestStore(t)
	mgr := New(st, t.TempDir(), nil)

	svc := ModelService{
		Metadata: Metadata{Name: "bad-svc"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromArtifact: &ArtifactRef{}},
			Runtime: RuntimeSpec{Mode: "local", Port: 8000}, // no Command
		},
	}
	err := mgr.Deploy(context.Background(), svc, t.TempDir())
	if err == nil {
		t.Error("expected error for missing command")
	}
}

func TestDeployLocal_missingPort(t *testing.T) {
	st := openTestStore(t)
	mgr := New(st, t.TempDir(), nil)

	svc := ModelService{
		Metadata: Metadata{Name: "bad-svc2"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromArtifact: &ArtifactRef{}},
			Runtime: RuntimeSpec{Mode: "local", Command: []string{"echo"}}, // no Port
		},
	}
	err := mgr.Deploy(context.Background(), svc, t.TempDir())
	if err == nil {
		t.Error("expected error for missing port")
	}
}

func TestDeploy_unknownMode(t *testing.T) {
	st := openTestStore(t)
	mgr := New(st, t.TempDir(), nil)

	svc := ModelService{
		Metadata: Metadata{Name: "bad-svc3"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromArtifact: &ArtifactRef{}},
			Runtime: RuntimeSpec{Mode: "docker", Command: []string{"echo"}, Port: 8000},
		},
	}
	err := mgr.Deploy(context.Background(), svc, t.TempDir())
	if err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestDeploy_k8sModeWithoutClient(t *testing.T) {
	st := openTestStore(t)
	mgr := New(st, t.TempDir(), nil) // no k8s client

	svc := ModelService{
		Metadata: Metadata{Name: "k8s-svc"},
		Spec: ModelServiceSpec{
			Model:   ModelRef{FromArtifact: &ArtifactRef{}},
			Runtime: RuntimeSpec{Mode: "k8s", Command: []string{"tritonserver"}, Port: 8000},
		},
	}
	err := mgr.Deploy(context.Background(), svc, "s3://bucket/model")
	if err == nil {
		t.Error("expected error when k8s clientset is nil")
	}
}

func TestStop_notFound(t *testing.T) {
	st := openTestStore(t)
	mgr := New(st, t.TempDir(), nil)

	err := mgr.Stop(context.Background(), "no-such-service")
	if err == nil {
		t.Error("expected error for non-existent service")
	}
}
