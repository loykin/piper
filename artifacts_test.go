package piper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/internal/artifact"
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
	cleanupOrphanArtifacts(context.Background(), checker, nil, outputDir)

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
	cleanupOrphanArtifacts(context.Background(), checker, nil, outputDir, "models")

	if _, err := os.Stat(filepath.Join(outputDir, "models")); err != nil {
		t.Fatalf("excluded dir 'models' should survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "run-orphan-old")); !os.IsNotExist(err) {
		t.Fatalf("non-excluded orphan should still be removed, err=%v", err)
	}
}

func TestCleanupOrphanArtifacts_skipsWhenBlobstoreConfigured(t *testing.T) {
	outputDir := t.TempDir()
	dir := filepath.Join(outputDir, "run-orphan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	checker := &fakeExistenceChecker{}
	// A non-nil storage.Store means artifacts live in a blobstore, not
	// outputDir — the local sweep must not touch anything in that mode.
	cleanupOrphanArtifacts(context.Background(), checker, storage.NewMemStore(), outputDir)

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory should be untouched when a blobstore is configured: %v", err)
	}
}
