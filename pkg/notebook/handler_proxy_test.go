package notebook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/pkg/project"
)

// TestProxyNotebookReverseProxiesDirectRuntimeEndpoint is a regression test:
// direct-runtime notebooks (docker/baremetal, see
// pkg/notebook/dispatch/localdriver) report a plain http:// Endpoint, not a
// tunnel:// one. proxyNotebook must reverse-proxy straight to it instead of
// trying to parse it as tunnel://<agentID>?target=<addr> and failing with
// "invalid notebook endpoint".
//
// Routed through a real httptest.Server rather than a bare
// gin.CreateTestContext recorder: httputil.ReverseProxy (used inside
// tunnelproxy.ServeReverseProxy) probes the ResponseWriter for
// http.CloseNotifier support, and gin's writer wrapping a plain
// httptest.ResponseRecorder panics on that probe — an artifact of testing
// gin handlers directly, unrelated to the fix itself.
func TestProxyNotebookReverseProxiesDirectRuntimeEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-body"))
	}))
	defer upstream.Close()

	repo := newFakeRepo()
	if err := repo.Create(context.Background(), &NotebookServer{
		ProjectID: "proj", Name: "nb", Status: StatusRunning, Endpoint: upstream.URL, Token: "tok",
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(HandlerDeps{Notebooks: repo})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/projects/:project_id/notebooks/:name/proxy/*path", func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: c.Param("project_id")})
		c.Request = c.Request.WithContext(ctx)
		h.proxyNotebook(c)
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/projects/proj/notebooks/nb/proxy/lab")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	if string(body) != "upstream-body" {
		t.Fatalf("body = %q, want upstream-body", body)
	}
}

// TestListVolumeFilesUsesLocalFilesystemWhenWorkerIDEmpty is a regression
// test: direct-runtime notebook volumes (see
// pkg/notebook/dispatch/localdriver.Driver.ProvisionVolume) deliberately
// leave NotebookVolume.WorkerID empty, matching its documented "no specific
// worker owns it" meaning. listVolumeFiles must take the local-filesystem
// walk in that case rather than RPCing a worker ID that has no real
// connection registered for it.
func TestListVolumeFilesUsesLocalFilesystemWhenWorkerIDEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	vols := newFakeVols()
	if err := vols.Create(context.Background(), &NotebookVolume{ID: "vol-1", ProjectID: "proj", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(HandlerDeps{Volumes: vols, Notebooks: newFakeRepo()})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/notebook-volumes/vol-1/files", nil)
	ctx := project.WithContext(req.Context(), project.Context{ID: "proj"})
	c.Request = req.WithContext(ctx)
	c.Params = gin.Params{{Key: "id", Value: "vol-1"}}

	h.listVolumeFiles(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var files []string
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f == "hello.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files = %v, want to contain hello.txt", files)
	}
}
