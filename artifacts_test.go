package piper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/storage"
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
