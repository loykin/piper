package servingworker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestRouter builds the gin router for a Worker without starting a real HTTP server.
func newTestRouter(w *Worker) http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/", w.healthHandler)
	r.POST("/deploy", w.deployHandler)
	r.DELETE("/service/:name", w.stopHandler)
	r.POST("/service/:name/restart", w.restartHandler)
	return r
}

func TestServingWorker_HealthHandler(t *testing.T) {
	w := New(Config{ID: "test-id", Addr: ":7700"})
	router := newTestRouter(w)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET / status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["id"] != "test-id" {
		t.Errorf("id = %v, want test-id", body["id"])
	}
}

func TestServingWorker_StopHandlerNotFound(t *testing.T) {
	w := New(Config{ID: "test-id", Addr: ":7700"})
	router := newTestRouter(w)

	req := httptest.NewRequest(http.MethodDelete, "/service/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE /service/nonexistent status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServingWorker_RestartHandlerNotFound(t *testing.T) {
	// When the service doesn't exist, restartHandler kills nothing but still returns 204.
	w := New(Config{ID: "test-id", Addr: ":7700"})
	router := newTestRouter(w)

	req := httptest.NewRequest(http.MethodPost, "/service/nonexistent/restart", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// restartHandler always returns 204 (the master follows up with a new deploy).
	if rec.Code != http.StatusNoContent {
		t.Errorf("POST /service/nonexistent/restart status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestServingWorker_DeployInvalidJSON(t *testing.T) {
	// Use a fake master so callbackStatus doesn't fail.
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()

	wk := New(Config{ID: "test-id", Addr: ":7700", MasterURL: master.URL})
	router := newTestRouter(wk)

	req := httptest.NewRequest(http.MethodPost, "/deploy", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /deploy with invalid JSON status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestServingWorker_DeployCommandNotFound(t *testing.T) {
	// A valid JSON payload whose command binary doesn't exist → StartProcess fails → 500.
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()

	wk := New(Config{ID: "test-id", Addr: ":7700", MasterURL: master.URL})
	router := newTestRouter(wk)

	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: test-svc
spec:
  runtime:
    command: ["__binary_that_does_not_exist__"]
    port: 19999
`
	body, _ := json.Marshal(map[string]any{
		"yaml":       yamlPayload,
		"local_path": "/tmp/model",
		"master_url": master.URL,
	})

	req := httptest.NewRequest(http.MethodPost, "/deploy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("POST /deploy with missing binary status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServingWorker_DeployEmptyCommand(t *testing.T) {
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()

	wk := New(Config{ID: "test-id", Addr: ":7700", MasterURL: master.URL})
	router := newTestRouter(wk)

	// YAML with no command → 400.
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-cmd-svc
spec:
  runtime:
    port: 8080
`
	body, _ := json.Marshal(map[string]any{
		"yaml": yamlPayload,
	})

	req := httptest.NewRequest(http.MethodPost, "/deploy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /deploy with empty command status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestServingWorker_DeployNoPort(t *testing.T) {
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()

	wk := New(Config{ID: "test-id", Addr: ":7700", MasterURL: master.URL})
	router := newTestRouter(wk)

	// YAML with command but no port → 400.
	yamlPayload := `apiVersion: piper/v1
kind: ModelService
metadata:
  name: no-port-svc
spec:
  runtime:
    command: ["echo"]
`
	body, _ := json.Marshal(map[string]any{
		"yaml": yamlPayload,
	})

	req := httptest.NewRequest(http.MethodPost, "/deploy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /deploy with no port status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
