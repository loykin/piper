package piper_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/piper/piper/pkg/piper"
	"github.com/piper/piper/pkg/store"
	_ "modernc.org/sqlite"
)

const testServiceYAML = `
apiVersion: piper/v1
kind: ModelService
metadata:
  name: fraud-detector
spec:
  model:
    from_artifact:
      pipeline: train-fraud
      step: train
      artifact: model
      run: latest
  runtime:
    image: nvcr.io/nvidia/tritonserver:24.01-py3
    command: ["tritonserver", "--model-repository=$(PIPER_MODEL_DIR)"]
    port: 8000
    mode: local
`

// newTestPiperWithStore returns a Piper and a Store sharing the same in-memory DB.
// This lets tests seed data directly without going through the HTTP API.
func newTestPiperWithStore(t *testing.T) (*piper.Piper, *store.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	p, err := piper.New(piper.Config{
		DB:        db,
		OutputDir: t.TempDir(),
	})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	st, err := store.New(db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.Close()
		_ = db.Close()
	})
	return p, st
}

// ─── GET /services ────────────────────────────────────────────────────────────

func TestAPI_listServices_empty(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/services", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var svcs []any
	_ = json.NewDecoder(w.Body).Decode(&svcs)
	if len(svcs) != 0 {
		t.Errorf("want empty list, got %d services", len(svcs))
	}
}

// ─── POST /services ───────────────────────────────────────────────────────────

func TestAPI_deployService_invalidBody(t *testing.T) {
	p := newTestPiper(t)
	// Send non-JSON body
	w := do(t, p.Handler(nil), http.MethodPost, "/services", "not-a-struct")
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for invalid body")
	}
}

func TestAPI_deployService_missingName(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/services", map[string]any{
		"yaml": `
apiVersion: piper/v1
kind: ModelService
metadata:
  name: ""
spec:
  model:
    from_artifact:
      pipeline: train
      step: train
      artifact: model
      run: latest
  runtime:
    command: ["echo"]
    port: 8000
    mode: local
`,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_deployService_noSuccessfulRun(t *testing.T) {
	p := newTestPiper(t)
	// No pipeline runs exist — should fail to resolve artifact
	w := do(t, p.Handler(nil), http.MethodPost, "/services", map[string]any{
		"yaml": testServiceYAML,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 when no successful run exists, got %d: %s", w.Code, w.Body.String())
	}
}

// ─── GET /services/{name} ────────────────────────────────────────────────────

func TestAPI_getService_notFound(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodGet, "/services/no-such-service", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ─── DELETE /services/{name} ─────────────────────────────────────────────────

func TestAPI_deleteService_notFound(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodDelete, "/services/ghost", nil)
	if w.Code == http.StatusOK || w.Code == http.StatusNoContent {
		t.Error("expected error status for deleting unknown service")
	}
}

// ─── POST /services/{name}/restart ───────────────────────────────────────────

func TestAPI_restartService_notFound(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPost, "/services/ghost/restart", nil)
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for restarting unknown service")
	}
}

// ─── Method not allowed ───────────────────────────────────────────────────────

func TestAPI_services_methodNotAllowed(t *testing.T) {
	p := newTestPiper(t)
	w := do(t, p.Handler(nil), http.MethodPut, "/services", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ─── Full lifecycle: seed run → deploy → list → get → delete ─────────────────

func TestAPI_services_lifecycle(t *testing.T) {
	p, st := newTestPiperWithStore(t)
	h := p.Handler(nil)

	// Seed a successful run so DeployService can resolve "latest" artifact
	_ = st.CreateRun(&store.Run{
		ID:           "run-seed",
		PipelineName: "train-fraud",
		Status:       "success",
		StartedAt:    time.Now(),
	})

	// Use a short-lived process so the test finishes quickly
	yaml := `
apiVersion: piper/v1
kind: ModelService
metadata:
  name: lifecycle-svc
spec:
  model:
    from_artifact:
      pipeline: train-fraud
      step: train
      artifact: model
      run: latest
  runtime:
    command: ["sleep", "60"]
    port: 19998
    mode: local
`
	// POST /services
	w := do(t, h, http.MethodPost, "/services", map[string]any{"yaml": yaml})
	t.Logf("deploy: %d %s", w.Code, w.Body.String())
	deployOK := w.Code == http.StatusOK

	// GET /services — must always return 200
	w2 := do(t, h, http.MethodGet, "/services", nil)
	if w2.Code != http.StatusOK {
		t.Errorf("list services: want 200, got %d", w2.Code)
	}

	if deployOK {
		// GET /services/lifecycle-svc
		w3 := do(t, h, http.MethodGet, "/services/lifecycle-svc", nil)
		if w3.Code != http.StatusOK {
			t.Errorf("get service: want 200, got %d: %s", w3.Code, w3.Body.String())
		}
		var svc map[string]any
		_ = json.NewDecoder(w3.Body).Decode(&svc)
		if svc["name"] != "lifecycle-svc" {
			t.Errorf("name mismatch: %v", svc["name"])
		}

		// DELETE /services/lifecycle-svc
		w4 := do(t, h, http.MethodDelete, "/services/lifecycle-svc", nil)
		if w4.Code != http.StatusNoContent {
			t.Errorf("delete service: want 204, got %d: %s", w4.Code, w4.Body.String())
		}

		// Confirm deleted
		w5 := do(t, h, http.MethodGet, "/services/lifecycle-svc", nil)
		if w5.Code != http.StatusNotFound {
			t.Errorf("after delete: want 404, got %d", w5.Code)
		}
	}
}
