package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loykin/piper/pkg/storage"
)

func TestArtifactURIForRemoteServing(t *testing.T) {
	tests := []struct {
		name       string
		storageURL string
		want       string
		wantErr    string
	}{
		{name: "s3", storageURL: "s3://models", want: "s3://models/run-1/train/model"},
		{name: "http unsupported", storageURL: "https://piper.example/api", wantErr: "requires s3"},
		{name: "file unsupported", storageURL: "file:///tmp/artifacts", wantErr: "cannot provide artifact URIs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&localResolver{storageURL: tt.storageURL}).artifactURI("run-1/train/model")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("artifactURI() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("artifactURI() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ─── resolveLocal (fed.md §13.6 workspace vs artifact repository) ──────────

func TestResolveLocal_LocalStorePrefersRepositoryOverWorkspace(t *testing.T) {
	outputDir := t.TempDir()
	ls, err := storage.NewLocal(filepath.Join(outputDir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := ls.Put(ctx, "run-1/train/model/weights.bin", strings.NewReader("repository-copy"), -1); err != nil {
		t.Fatal(err)
	}
	// A stale/different workspace copy exists too — resolveLocal must not
	// return this one when a LocalStore is configured.
	workspaceDir := filepath.Join(outputDir, "run-1", "train")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "weights.bin"), []byte("workspace-copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &localResolver{outputDir: outputDir, store: ls}
	resolved, err := r.Resolve(ctx, "pl", "train", "model", "run-1", TargetLocal)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(resolved.LocalPath, "weights.bin"))
	if err != nil {
		t.Fatalf("read resolved path: %v", err)
	}
	if string(data) != "repository-copy" {
		t.Fatalf("content = %q, want the repository copy, not the workspace one", string(data))
	}
}

func TestResolveLocal_RemoteStoreStagesLocalCacheAndReusesIt(t *testing.T) {
	outputDir := t.TempDir()
	ms := storage.NewMemStore()
	ctx := context.Background()
	if err := ms.Put(ctx, "run-1/train/model/weights.bin", strings.NewReader("from-remote"), -1); err != nil {
		t.Fatal(err)
	}

	r := &localResolver{outputDir: outputDir, store: ms}
	resolved, err := r.Resolve(ctx, "pl", "train", "model", "run-1", TargetLocal)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(outputDir, CacheDirName, "run-1", "train", "model")
	if resolved.LocalPath != wantDir {
		t.Fatalf("LocalPath = %q, want %q", resolved.LocalPath, wantDir)
	}
	data, err := os.ReadFile(filepath.Join(resolved.LocalPath, "weights.bin"))
	if err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
	if string(data) != "from-remote" {
		t.Fatalf("content = %q, want from-remote", string(data))
	}

	// Bug C regression: this is exactly what happens once the runner has
	// already deleted the producing step's workspace (cleanWorkdir=true for
	// a non-local store) — the remote copy is gone from the source too, so a
	// second resolution must reuse the cache rather than re-downloading.
	if err := ms.Delete(ctx, "run-1/train/model/weights.bin"); err != nil {
		t.Fatal(err)
	}
	resolved2, err := r.Resolve(ctx, "pl", "train", "model", "run-1", TargetLocal)
	if err != nil {
		t.Fatalf("second resolve should reuse the cache, got error: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(resolved2.LocalPath, "weights.bin"))
	if err != nil {
		t.Fatalf("cached file missing on second resolve: %v", err)
	}
	if string(data2) != "from-remote" {
		t.Fatalf("cached content = %q, want from-remote", string(data2))
	}
}

func TestResolveLocal_NoStoreFallsBackToWorkspace(t *testing.T) {
	outputDir := t.TempDir()
	workspaceDir := filepath.Join(outputDir, "run-1", "train")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "weights.bin"), []byte("workspace-only"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &localResolver{outputDir: outputDir, store: nil}
	resolved, err := r.Resolve(context.Background(), "pl", "train", "model", "run-1", TargetLocal)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.LocalPath != workspaceDir {
		t.Fatalf("LocalPath = %q, want %q (legacy no-store fallback)", resolved.LocalPath, workspaceDir)
	}
}
