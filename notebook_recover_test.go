package piper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

// A baremetal notebook whose process died while the server was down must not
// stay "running" after the server starts again: the local driver's Recover
// has to run at startup (k8s reconciles through Observe).
func TestNotebookLocalRuntimeRecoversOnStartup(t *testing.T) {
	ctx := context.Background()
	repos, err := storemod.Open(filepath.Join(t.TempDir(), "piper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	if err := repos.Project.Create(ctx, &project.Project{ID: "default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Notebook.Create(ctx, &notebook.NotebookServer{
		ProjectID: "default", Name: "nb", Status: notebook.StatusRunning, RuntimeID: notebookLocalRuntimeID,
	}); err != nil {
		t.Fatal(err)
	}

	// What the process driver leaves behind for a notebook it started: a meta
	// file, here with no live process behind it.
	root := t.TempDir()
	metaDir := filepath.Join(root, ".piper", "processes")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"project_id": "default", "name": "nb", "runtime_name": "default__nb", "port": 19999})
	if err := os.WriteFile(filepath.Join(metaDir, "x.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Runtime: RuntimeConfig{Type: RuntimeBaremetal}, Notebook: NotebookRuntimeConfig{NotebooksRoot: root, PortRange: "19990-19999"}}
	if _, err := composeNotebookRuntime(cfg, repos, nil); err != nil {
		t.Fatal(err)
	}
	nb, err := repos.Notebook.Get(ctx, "default", "nb")
	if err != nil {
		t.Fatal(err)
	}
	if nb.Status != notebook.StatusStopped {
		t.Fatalf("status after startup = %q, want %q", nb.Status, notebook.StatusStopped)
	}
}
