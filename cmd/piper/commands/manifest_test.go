package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
	"github.com/loykin/piper/internal/manifestmigrate"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/template"
)

const staleTemplateYAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: pl
spec:
  defaults:
    driver:
      placement:
        worker: pipeline-worker-1
`

const staleNotebookYAMLForCmd = `apiVersion: piper/v1
kind: Notebook
metadata:
  name: nb
spec:
  driver:
    placement:
      worker: gpu-1
`

func writeManifestTestConfig(t *testing.T, dbPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piper.yaml")
	body := "version: 4\nserver:\n  db:\n    driver: sqlite\n    path: " + dbPath + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManifestMigrateCmd_DryRunThenApply_RealSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "piper.db")

	// Seed a real sqlite DB with one stale pipeline template and one stale
	// notebook, through the same repository interfaces the server itself uses.
	seed, err := storemod.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	ctx := context.Background()
	if err := seed.Project.Create(ctx, &project.Project{ID: "proj-a", Name: "proj-a"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := seed.PipelineTemplate.Create(ctx, &template.Template{
		ProjectID: "proj-a", Name: "pl", Version: 1, YAML: staleTemplateYAML,
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := seed.Notebook.Create(ctx, &notebook.NotebookServer{
		ProjectID: "proj-a", Name: "nb", YAML: staleNotebookYAMLForCmd,
	}); err != nil {
		t.Fatalf("create notebook: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	configPath := writeManifestTestConfig(t, dbPath)

	// Dry run: reports both findings, changes nothing.
	{
		loader := cliconfig.NewLoader()
		loader.SetConfigFile(configPath)
		cmd := newManifestMigrateCmd(loader)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(nil)
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("dry-run RunE: %v", err)
		}
		report := out.String()
		if !strings.Contains(report, "[pipeline] project=proj-a name=pl version=1") {
			t.Fatalf("dry-run report missing pipeline finding:\n%s", report)
		}
		if !strings.Contains(report, "[notebook] project=proj-a name=nb") {
			t.Fatalf("dry-run report missing notebook finding:\n%s", report)
		}
		if strings.Contains(report, "fixed") {
			t.Fatalf("dry-run report must not claim anything was fixed:\n%s", report)
		}
	}

	// Verify dry-run didn't touch the DB.
	{
		check, err := storemod.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		templates, err := check.PipelineTemplate.List(ctx, "proj-a", template.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(templates) != 1 {
			t.Fatalf("templates = %d, want 1 (dry-run must not create a new version)", len(templates))
		}
		_ = check.Close()
	}

	// Apply: fixes both, verify via a fresh connection.
	{
		loader := cliconfig.NewLoader()
		loader.SetConfigFile(configPath)
		cmd := newManifestMigrateCmd(loader)
		if err := cmd.Flags().Set("apply", "true"); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("apply RunE: %v", err)
		}
		report := out.String()
		if !strings.Contains(report, "fixed: created version 2") {
			t.Fatalf("apply report missing pipeline fix confirmation:\n%s", report)
		}
		if !strings.Contains(report, "[notebook] project=proj-a name=nb — fixed") {
			t.Fatalf("apply report missing notebook fix confirmation:\n%s", report)
		}
	}

	check, err := storemod.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = check.Close() })

	templates, err := check.PipelineTemplate.List(ctx, "proj-a", template.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 {
		t.Fatalf("templates = %d, want 2 (v1 kept + v2 created)", len(templates))
	}
	for _, tpl := range templates {
		if tpl.Version == 1 && tpl.YAML != staleTemplateYAML {
			t.Fatal("version 1 must remain byte-for-byte untouched")
		}
		if tpl.Version == 2 {
			if _, changed, _ := manifestmigrate.StripPlacementWorkerLabel(tpl.YAML); changed {
				t.Fatal("version 2 should already be clean")
			}
		}
	}

	nb, err := check.Notebook.Get(ctx, "proj-a", "nb")
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, _ := manifestmigrate.StripPlacementWorkerLabel(nb.YAML); changed {
		t.Fatal("notebook YAML should already be clean after apply")
	}

	// Re-running Scan against the now-clean DB should find nothing left.
	findings, err := manifestmigrate.Scan(ctx, check)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("post-apply scan findings = %d, want 0: %+v", len(findings), findings)
	}
}
