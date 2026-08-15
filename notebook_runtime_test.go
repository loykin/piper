//go:build e2e

// This test drives the real baremetal (process) notebook driver, which
// creates a genuine Python venv and pip-installs jupyter into it on first
// start (see pkg/notebook/worker/driver/process) — slow and
// network-dependent, so it's gated behind the e2e build tag like this
// repo's other root-level //go:build e2e tests (e2e_test.go,
// membertunnel_e2e_test.go).
package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

// TestNotebookBaremetalDirectRuntimeLifecycle proves fed.md §13.6's
// prerequisite: the Notebook domain gets the same in-process direct-runtime
// treatment Pipeline already got in §13.2. A notebook created through the
// real HTTP API on a Piper configured with Runtime.Type=baremetal must be
// launched by pkg/notebook/dispatch/localdriver directly (no remote
// worker/tunnel involved) and reach status=running with a plain
// http://127.0.0.1:<port> endpoint, then be cleanly deleted.
func TestNotebookBaremetalDirectRuntimeLifecycle(t *testing.T) {
	const projectID = "proj-notebook-direct"

	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Storage:   StorageConfig{Disabled: true},
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{Concurrency: 2, MetaDir: t.TempDir()},
		},
		Notebook: NotebookRuntimeConfig{
			NotebooksRoot: t.TempDir(),
			PortRange:     "19100-19110",
		},
	})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	router := p.Handler(nil)

	yaml := `apiVersion: piper/v1
kind: Notebook
metadata:
  name: direct-nb
spec:
  driver:
    placement:
      runtime: baremetal
`
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/notebooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create notebook status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// Poll GET /notebooks/:name until the async localdriver.Start reports
	// running via ReportStatus -> nbMgr.UpdateStatus (never trusting the
	// synchronous create response, matching Manager's own contract).
	// A generous deadline: first run creates a venv and pip-installs
	// jupyter (see pkg/notebook/worker/driver/process), matching the
	// 3-minute budget driver_e2e_test.go gives the same operation.
	deadline := time.Now().Add(3 * time.Minute)
	var nb notebook.NotebookServer
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/notebooks/direct-nb", nil)
		getRec := httptest.NewRecorder()
		router.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get notebook status = %d: %s", getRec.Code, getRec.Body.String())
		}
		if err := json.NewDecoder(getRec.Body).Decode(&nb); err != nil {
			t.Fatal(err)
		}
		if nb.Status == notebook.StatusRunning {
			break
		}
		if nb.Status == notebook.StatusFailed {
			t.Fatalf("notebook reached status=failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("notebook did not reach status=running in time, last status=%q", nb.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if nb.RuntimeID != notebookLocalRuntimeID {
		t.Fatalf("RuntimeID = %q, want %q (direct-runtime local identity)", nb.RuntimeID, notebookLocalRuntimeID)
	}
	if !strings.HasPrefix(nb.Endpoint, "http://127.0.0.1:") {
		t.Fatalf("Endpoint = %q, want http://127.0.0.1:<port> (no tunnel:// scheme in direct mode)", nb.Endpoint)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/notebooks/direct-nb", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete notebook status = %d, want 204: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/notebooks/direct-nb", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get notebook after delete status = %d, want 404: %s", getRec.Code, getRec.Body.String())
	}
}
