package piper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/storage"
)

// ── resolveModelURI ───────────────────────────────────────────────────────────

func TestResolveModelURI_s3NoStore(t *testing.T) {
	p := &Piper{
		cfg:   Config{OutputDir: t.TempDir()},
		store: nil,
	}
	_, err := p.resolveModelURI(context.Background(), "svc", "s3://bucket/model", artifact.TargetLocal)
	if err == nil {
		t.Fatal("expected error when store is nil, got nil")
	}
	if !strings.Contains(err.Error(), "storage") {
		t.Fatalf("error %q should mention storage", err)
	}
}

func TestResolveModelURI_s3K8sTarget(t *testing.T) {
	p := &Piper{cfg: Config{OutputDir: t.TempDir()}}
	resolved, err := p.resolveModelURI(context.Background(), "svc", "s3://bucket/model/v1", artifact.TargetS3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.S3URI != "s3://bucket/model/v1" {
		t.Fatalf("S3URI = %q, want s3://bucket/model/v1", resolved.S3URI)
	}
}

func TestResolveModelURI_s3LocalDownload(t *testing.T) {
	ctx := context.Background()
	ms := storage.NewMemStore()
	_ = ms.Put(ctx, "model/weights.bin", strings.NewReader("weights"), int64(len("weights")))

	modelDir := t.TempDir()
	p := &Piper{
		cfg:   Config{OutputDir: t.TempDir(), Serving: ServingConfig{ModelDir: modelDir}},
		store: ms,
	}
	resolved, err := p.resolveModelURI(ctx, "mysvc", "s3://anybucket/model/weights.bin", artifact.TargetLocal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.LocalPath == "" {
		t.Fatal("expected LocalPath to be set")
	}
	// DownloadDir downloads "model/weights.bin" → modelDir/mysvc/weights.bin
	data, err := os.ReadFile(filepath.Join(resolved.LocalPath, "weights.bin"))
	if err != nil {
		t.Fatalf("downloaded file not found: %v", err)
	}
	if string(data) != "weights" {
		t.Fatalf("content = %q, want %q", string(data), "weights")
	}
}

func TestResolveModelURI_s3MissingKey(t *testing.T) {
	ctx := context.Background()
	ms := storage.NewMemStore()
	p := &Piper{
		cfg:   Config{OutputDir: t.TempDir()},
		store: ms,
	}
	_, err := p.resolveModelURI(ctx, "svc", "s3://bucket", artifact.TargetLocal)
	if err == nil {
		t.Fatal("expected error for s3 URI without key")
	}
}

func TestResolveModelURI_fileScheme(t *testing.T) {
	dir := t.TempDir()
	p := &Piper{cfg: Config{OutputDir: t.TempDir()}}
	resolved, err := p.resolveModelURI(context.Background(), "svc", "file://"+dir, artifact.TargetLocal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.LocalPath != dir {
		t.Fatalf("LocalPath = %q, want %q", resolved.LocalPath, dir)
	}
}

// ── cleanupOrphanArtifacts ──────────────────────────────────────────────────

type fakeExistenceChecker struct {
	existing map[string]bool
}

func (f *fakeExistenceChecker) ExistingIDs(_ context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if f.existing[id] {
			out[id] = true
		}
	}
	return out, nil
}

func TestCleanupOrphanArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	mkDir := func(name string, age time.Duration) {
		t.Helper()
		dir := filepath.Join(outputDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		modTime := time.Now().Add(-age)
		if err := os.Chtimes(dir, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	mkDir("run-orphan-old", 20*time.Minute)  // old, no DB row -> should be removed
	mkDir("run-known-old", 20*time.Minute)   // old, has DB row -> should be kept
	mkDir("run-orphan-fresh", 1*time.Minute) // fresh, no DB row -> too new, kept for now

	checker := &fakeExistenceChecker{existing: map[string]bool{"run-known-old": true}}
	cleanupOrphanArtifacts(context.Background(), checker, outputDir)

	assertExists := func(name string, want bool) {
		t.Helper()
		_, err := os.Stat(filepath.Join(outputDir, name))
		got := err == nil
		if got != want {
			t.Errorf("%s exists = %v, want %v (err=%v)", name, got, want, err)
		}
	}
	assertExists("run-orphan-old", false)
	assertExists("run-known-old", true)
	assertExists("run-orphan-fresh", true)
}

func TestCleanupOrphanArtifacts_excludesNamedDirs(t *testing.T) {
	outputDir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"models", "run-orphan-old"} {
		dir := filepath.Join(outputDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	checker := &fakeExistenceChecker{}
	cleanupOrphanArtifacts(context.Background(), checker, outputDir, "models")

	if _, err := os.Stat(filepath.Join(outputDir, "models")); err != nil {
		t.Fatalf("excluded dir 'models' should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan-old")); !os.IsNotExist(err) {
		t.Fatalf("non-excluded orphan should still be removed, err=%v", err)
	}
}

// TestCleanupOrphanArtifacts_SweepsWorkspaceEvenWithStoreConfigured is a
// regression test for fed.md §13.6: the run workspace (outputDir) and the
// artifact repository (Store) have independent lifecycles, so an orphaned
// workspace directory must still be swept regardless of whether a Store is
// configured — as long as the Store's own root is excluded (the caller's
// job; see Piper.cleanupOrphanArtifacts, which computes that exclusion).
func TestCleanupOrphanArtifacts_SweepsWorkspaceEvenWithStoreConfigured(t *testing.T) {
	outputDir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"store", "run-orphan"} {
		dir := filepath.Join(outputDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	checker := &fakeExistenceChecker{}
	// The caller excludes "store" (the Store's own root) explicitly, exactly
	// as Piper.cleanupOrphanArtifacts does for a LocalStore rooted under
	// outputDir.
	cleanupOrphanArtifacts(context.Background(), checker, outputDir, "store")

	if _, err := os.Stat(filepath.Join(outputDir, "store")); err != nil {
		t.Fatalf("excluded store root should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan")); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed even with a store configured, err=%v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_ExcludesDefaultLocalStoreRoot exercises
// Piper.cleanupOrphanArtifacts (the method, not the standalone function
// tested above) end-to-end with the default configuration — no explicit
// storage.url — which provisions a LocalStore rooted at OutputDir/store
// (see resolveStorageURL). The method must compute that root and exclude it
// dynamically, not just rely on the caller passing "store" by convention.
func TestPiperCleanupOrphanArtifacts_ExcludesDefaultLocalStoreRoot(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	if _, ok := p.store.(*storage.LocalStore); !ok {
		t.Fatalf("expected default config to provision a LocalStore, got %T", p.store)
	}

	old := time.Now().Add(-time.Hour)
	orphanDir := filepath.Join(p.cfg.OutputDir, "run-orphan")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphanDir, old, old); err != nil {
		t.Fatal(err)
	}
	// Put something in the store so it's non-empty, then touch its mtime old
	// too — if the exclusion logic is broken, the sweep would delete this.
	ctx := context.Background()
	if err := p.store.Put(ctx, "some-run/step/artifact/file.txt", strings.NewReader("data"), -1); err != nil {
		t.Fatal(err)
	}

	p.cleanupOrphanArtifacts(ctx)

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should be removed, err=%v", err)
	}
	if _, err := p.store.Get(ctx, "some-run/step/artifact/file.txt"); err != nil {
		t.Fatalf("artifact repository content should survive the sweep: %v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_RelativeOutputDirExcludesStore is a
// regression test for a real bug found during local QA (fed.md §14): when
// OutputDir is a relative path (e.g. "./piper-data", the common case for a
// real deployment run from a working directory — see qa-baremetal's
// piper.yaml), filepath.Rel(p.cfg.OutputDir, ls.Root()) used to fail because
// ls.Root() is always absolute (storage.NewLocal calls filepath.Abs), and
// mixing a relative base with an absolute target is a filepath.Rel error.
// The exclusion was silently skipped on that error, so the orphan sweep
// deleted the LocalStore's own root directory — the artifact repository
// itself, including every artifact ever uploaded.
func TestPiperCleanupOrphanArtifacts_RelativeOutputDirExcludesStore(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	p := newTestPiper(t, Config{OutputDir: "./piper-data"})
	if _, ok := p.store.(*storage.LocalStore); !ok {
		t.Fatalf("expected default config to provision a LocalStore, got %T", p.store)
	}

	ctx := context.Background()
	if err := p.store.Put(ctx, "some-run/step/artifact/file.txt", strings.NewReader("data"), -1); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-time.Hour)
	orphanDir := filepath.Join(p.cfg.OutputDir, "run-orphan")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphanDir, old, old); err != nil {
		t.Fatal(err)
	}

	p.cleanupOrphanArtifacts(ctx)

	if _, err := p.store.Get(ctx, "some-run/step/artifact/file.txt"); err != nil {
		t.Fatalf("artifact repository content should survive the sweep with a relative OutputDir: %v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed, err=%v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_ExcludesResultsAndBaremetalMetaDir is a
// regression test for a real bug found during local QA (fed.md §14): the
// baremetal and docker direct-runtime drivers create a fixed ".results"
// directory directly under OutputDir to hold every task's result/task JSON
// (see pkg/pipeline/worker/driver/{baremetal,docker}.Start), and an operator
// may point runtime.baremetal.meta_dir at a subdirectory of OutputDir (as
// qa-baremetal's piper.yaml did: "./piper-data/pipeline-meta" under
// output_dir "./piper-data"). Neither name is a run ID, so before this fix
// the orphan sweep deleted both as "orphaned run directories" — wiping the
// baremetal driver's process bookkeeping and result history.
func TestPiperCleanupOrphanArtifacts_ExcludesResultsAndBaremetalMetaDir(t *testing.T) {
	outputDir := t.TempDir()
	p := newTestPiper(t, Config{
		OutputDir: outputDir,
		Runtime: RuntimeConfig{
			Type:      RuntimeBaremetal,
			Baremetal: BaremetalRuntimeConfig{MetaDir: filepath.Join(outputDir, "pipeline-meta")},
		},
	})

	old := time.Now().Add(-time.Hour)
	for _, name := range []string{".results", "pipeline-meta", "run-orphan"} {
		dir := filepath.Join(p.cfg.OutputDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}

	p.cleanupOrphanArtifacts(context.Background())

	for _, name := range []string{".results", "pipeline-meta"} {
		if _, err := os.Stat(filepath.Join(p.cfg.OutputDir, name)); err != nil {
			t.Fatalf("%s should survive the sweep: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.cfg.OutputDir, "run-orphan")); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed, err=%v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_ExcludesNotebookRoot prevents the orphan
// run sweep from deleting persistent notebook volumes when notebooks_root is
// nested under output_dir. A live QA notebook exposed this as both data loss
// and a tight log-tailer retry loop after its directory disappeared.
func TestPiperCleanupOrphanArtifacts_ExcludesNotebookRoot(t *testing.T) {
	outputDir := t.TempDir()
	notebooksRoot := filepath.Join(outputDir, "notebooks")
	p := newTestPiper(t, Config{
		OutputDir: outputDir,
		Notebook: NotebookRuntimeConfig{
			NotebooksRoot: notebooksRoot,
		},
	})

	old := time.Now().Add(-time.Hour)
	for _, dir := range []string{notebooksRoot, filepath.Join(outputDir, "run-orphan")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(notebooksRoot, "volume-data.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	p.cleanupOrphanArtifacts(context.Background())

	if _, err := os.Stat(filepath.Join(notebooksRoot, "volume-data.txt")); err != nil {
		t.Fatalf("notebook volume content should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan")); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed, err=%v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_ExcludesDefaultStatsSpool is a regression
// test for AT: the orphan run sweep used to have no exclusion for
// stats.spool.dir, so ten minutes after startup it deleted the external
// stats backend's disk spool (queued log/metric writes plus the global
// sequence file) out from under a live server, whether or not a spool
// outage was ever in progress. stats.spool.dir defaults to
// outputDir/stats-spool when unset, exactly like the "models" default this
// sweep already protects.
func TestPiperCleanupOrphanArtifacts_ExcludesDefaultStatsSpool(t *testing.T) {
	outputDir := t.TempDir()
	p := newTestPiper(t, Config{OutputDir: outputDir})

	old := time.Now().Add(-time.Hour)
	for _, dir := range []string{filepath.Join(outputDir, "stats-spool"), filepath.Join(outputDir, "run-orphan")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outputDir, "stats-spool", "sequence"), []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}

	p.cleanupOrphanArtifacts(context.Background())

	if _, err := os.Stat(filepath.Join(outputDir, "stats-spool", "sequence")); err != nil {
		t.Fatalf("default stats spool should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan")); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed, err=%v", err)
	}
}

// TestPiperCleanupOrphanArtifacts_ExcludesCustomStatsSpool is the
// stats.spool.dir-explicitly-set variant of the AT regression above.
func TestPiperCleanupOrphanArtifacts_ExcludesCustomStatsSpool(t *testing.T) {
	outputDir := t.TempDir()
	spoolDir := filepath.Join(outputDir, "custom-spool")
	p := newTestPiper(t, Config{
		OutputDir: outputDir,
		Stats:     StatsConfig{Spool: StatsSpoolConfig{Dir: spoolDir}},
	})

	old := time.Now().Add(-time.Hour)
	for _, dir := range []string{spoolDir, filepath.Join(outputDir, "run-orphan")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}

	p.cleanupOrphanArtifacts(context.Background())

	if _, err := os.Stat(spoolDir); err != nil {
		t.Fatalf("custom stats spool should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan")); !os.IsNotExist(err) {
		t.Fatalf("orphaned workspace directory should still be removed, err=%v", err)
	}
}

// ─── deleteArtifactsFromStore / deleteRunWorkspace (fed.md §13.6) ──────────

// TestDeleteRunWorkspace_IndependentOfStore is a regression test: before the
// fed.md §13.6 fix, run deletion only cleaned up whichever of {store,
// workspace} matched whether a store was configured — never both — so with
// the default LocalStore config, a deleted run's workspace directory leaked
// forever. deleteRunWorkspace must remove it regardless of what deleteArtifactsFromStore does.
func TestDeleteRunWorkspace_IndependentOfStore(t *testing.T) {
	outputDir := t.TempDir()
	runDir := filepath.Join(outputDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := deleteRunWorkspace(outputDir, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed, err=%v", err)
	}
	// Idempotent: removing an already-gone workspace is not an error.
	if err := deleteRunWorkspace(outputDir, "run-1"); err != nil {
		t.Fatalf("second delete should be a no-op, got: %v", err)
	}
}

func TestDeleteArtifactsFromStore_NilStoreIsNoop(t *testing.T) {
	if err := deleteArtifactsFromStore(context.Background(), nil, "run-1"); err != nil {
		t.Fatalf("nil store should be a no-op, got: %v", err)
	}
}

// ─── storage-backend mismatch diagnostic (docs/backend/develop.md's
// storage-identity stamp) ───────────────────────────────────────────────────

// createTestRun inserts a minimal Run row directly through the repository
// (bypassing the queue/runlifecycle machinery, which isn't needed to
// exercise the artifact-read mismatch diagnostic) with the given
// StorageBackend stamp, and returns a context carrying the default project.
func createTestRun(t *testing.T, p *Piper, runID, storageBackend string) context.Context {
	t.Helper()
	ctx := project.WithContext(context.Background(), project.Context{ID: project.DefaultID})
	r := &run.Run{
		ID:             runID,
		ProjectID:      project.DefaultID,
		PipelineName:   "mismatch-test",
		Status:         run.StatusSuccess,
		StartedAt:      time.Now().UTC(),
		StorageBackend: storageBackend,
	}
	if err := p.repos.Run.Create(ctx, r); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return ctx
}

func TestPiperArtifacts_List_StorageBackendMismatch(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	// The default config provisions a LocalStore; stamp the run with a
	// deliberately different identity so the live backend never matches.
	ctx := createTestRun(t, p, "run-mismatch", "s3:some-other-bucket")

	_, err := (&piperArtifacts{p: p}).List(ctx, "run-mismatch")
	if err == nil {
		t.Fatal("expected an error for a run with no artifacts under a mismatched storage backend")
	}
	if !errors.Is(err, memberclient.ErrStorageBackendMismatch) {
		t.Fatalf("expected memberclient.ErrStorageBackendMismatch, got: %v", err)
	}
}

func TestPiperArtifacts_List_EmptyStampNeverMismatches(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	// No StorageBackend set (predates this feature) — must never be flagged
	// as a mismatch, even though the run has no artifacts.
	ctx := createTestRun(t, p, "run-unstamped", "")

	result, err := (&piperArtifacts{p: p}).List(ctx, "run-unstamped")
	if err != nil {
		t.Fatalf("expected no error for an unstamped run, got: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected an empty artifact list, got %d entries", len(result))
	}
}

func TestPiperArtifacts_List_MatchingStampNeverMismatches(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	// Stamped with exactly the live identity — a run with genuinely no
	// artifacts must still resolve as a plain empty list, not an error.
	ctx := createTestRun(t, p, "run-matching", p.storageIdentity)

	result, err := (&piperArtifacts{p: p}).List(ctx, "run-matching")
	if err != nil {
		t.Fatalf("expected no error when the stamp matches the live backend, got: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected an empty artifact list, got %d entries", len(result))
	}
}

func TestPiperArtifacts_ServeDownload_StorageBackendMismatch(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := createTestRun(t, p, "run-mismatch-dl", "s3:some-other-bucket")

	req := httptest.NewRequest(http.MethodGet, "/runs/run-mismatch-dl/artifacts/step/missing.txt", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	(&piperArtifacts{p: p}).ServeDownload(rec, req, "run-mismatch-dl", "step", "missing.txt")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "storage_backend_mismatch") {
		t.Fatalf("body = %q, want it to mention storage_backend_mismatch", rec.Body.String())
	}
}

func TestPiperArtifacts_ServeDownload_GenericNotFoundWhenUnstamped(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir()})
	ctx := createTestRun(t, p, "run-unstamped-dl", "")

	req := httptest.NewRequest(http.MethodGet, "/runs/run-unstamped-dl/artifacts/step/missing.txt", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	(&piperArtifacts{p: p}).ServeDownload(rec, req, "run-unstamped-dl", "step", "missing.txt")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "storage_backend_mismatch") {
		t.Fatalf("body = %q should NOT mention storage_backend_mismatch for an unstamped run", rec.Body.String())
	}
}

func TestDeleteArtifactsFromStore_RemovesOnlyThatRunsKeys(t *testing.T) {
	ctx := context.Background()
	ms := storage.NewMemStore()
	if err := ms.Put(ctx, "run-1/step/artifact/file.txt", strings.NewReader("a"), -1); err != nil {
		t.Fatal(err)
	}
	if err := ms.Put(ctx, "run-2/step/artifact/file.txt", strings.NewReader("b"), -1); err != nil {
		t.Fatal(err)
	}
	if err := deleteArtifactsFromStore(ctx, ms, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.Get(ctx, "run-1/step/artifact/file.txt"); err == nil {
		t.Fatal("run-1's artifact should be gone")
	}
	if _, err := ms.Get(ctx, "run-2/step/artifact/file.txt"); err != nil {
		t.Fatalf("run-2's artifact should be untouched: %v", err)
	}
}
