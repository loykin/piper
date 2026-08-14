package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
)

// TestServingBaremetalDirectRuntimeLifecycle proves fed.md §13.6's second
// prerequisite: the Serving domain gets the same in-process direct-runtime
// treatment Pipeline (§13.2) and Notebook already got. A ModelService
// deployed through the real HTTP API on a Piper configured with
// Runtime.Type=baremetal must be launched by
// pkg/serving/dispatch/localdriver directly (no remote worker/tunnel
// involved), pass its own health check, and reach status=running with a
// plain http://127.0.0.1:<port> endpoint, then be cleanly deleted.
//
// Unlike the equivalent Notebook test, this uses a plain stdlib HTTP server
// as the "model" process (python3 -m http.server) instead of installing a
// real Jupyter venv, so it stays fast and needs no network access — no e2e
// build tag required.
func TestServingBaremetalDirectRuntimeLifecycle(t *testing.T) {
	const projectID = "proj-serving-direct"
	const port = 19277

	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Storage:   StorageConfig{Disabled: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
	})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	router := p.Handler(nil)

	modelDir := t.TempDir()
	yaml := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: direct-svc
spec:
  model:
    from_uri: "file://` + modelDir + `"
  run:
    command: ["python3", "-m", "http.server", "` + strconv.Itoa(port) + `"]
    port: ` + strconv.Itoa(port) + `
    health_path: /
  driver:
    placement:
      runtime: baremetal
`
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deploy service status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// Poll GET /services/:name until the async localdriver.Deploy's health
	// check reports running via ReportStatus -> servingMgr.UpdateStatus
	// (never trusting the synchronous deploy response's Status field alone,
	// matching Manager's own async contract).
	deadline := time.Now().Add(15 * time.Second)
	var svc serving.Service
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/services/direct-svc", nil)
		getRec := httptest.NewRecorder()
		router.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get service status = %d: %s", getRec.Code, getRec.Body.String())
		}
		if err := json.NewDecoder(getRec.Body).Decode(&svc); err != nil {
			t.Fatal(err)
		}
		if svc.Status == serving.StatusRunning {
			break
		}
		if svc.Status == serving.StatusFailed {
			t.Fatalf("service reached status=failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not reach status=running in time, last status=%q", svc.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if svc.WorkerID != servingLocalWorkerID {
		t.Fatalf("WorkerID = %q, want %q (direct-runtime local identity, not a remote worker)", svc.WorkerID, servingLocalWorkerID)
	}
	if !strings.HasPrefix(svc.Endpoint, "http://localhost:") && !strings.HasPrefix(svc.Endpoint, "http://127.0.0.1:") {
		t.Fatalf("Endpoint = %q, want a local endpoint (no tunnel:// scheme in direct mode)", svc.Endpoint)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/services/direct-svc", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete service status = %d, want 204: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/services/direct-svc", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get service after delete status = %d, want 404: %s", getRec.Code, getRec.Body.String())
	}
}
