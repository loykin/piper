package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/storage"
)

// stubRunRepoWithStamp is a minimal run.Repository double whose only
// meaningful behavior is Get returning a fixed StorageBackend stamp — the
// only method storageBackendMismatch calls. Any other method panics via the
// nil embedded interface if accidentally invoked, which is fine: no test
// using it should reach one.
type stubRunRepoWithStamp struct {
	run.Repository
	storageBackend string
}

func (s *stubRunRepoWithStamp) Get(_ context.Context, _, id string) (*run.Run, error) {
	return &run.Run{ID: id, StorageBackend: s.storageBackend}, nil
}

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

// TestResolveLocal_StorageBackendMismatchCaughtEvenWhenDataExistsAtTheKey is
// the core regression for the adversarial-review finding that the mismatch
// check only ran as a fallback after a not-found: if the live backend
// happens to hold *different* data at the exact same runID/step/artifact
// key (e.g. a migration to a backend pre-seeded from a stale copy), a
// check-only-on-failure design would silently return that wrong data as if
// it were this run's own — a wrong model deployed as if it were correct in
// the ModelService from_artifact case. The live store here genuinely has
// data at the key (Get would succeed), but the run's stamp doesn't match
// the live identity, so Resolve must still refuse it up front.
func TestResolveLocal_StorageBackendMismatchCaughtEvenWhenDataExistsAtTheKey(t *testing.T) {
	outputDir := t.TempDir()
	ls, err := storage.NewLocal(filepath.Join(outputDir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The live backend genuinely has *something* at this exact key — data
	// that belongs to a different backend generation, not this run.
	if err := ls.Put(ctx, "run-1/train/model/weights.bin", strings.NewReader("wrong-generation-data"), -1); err != nil {
		t.Fatal(err)
	}

	r := &localResolver{
		outputDir:    outputDir,
		store:        ls,
		liveIdentity: "s3:new-bucket",
		runRepo:      &stubRunRepoWithStamp{storageBackend: "s3:old-bucket"},
	}
	_, err = r.Resolve(ctx, "pl", "train", "model", "run-1", TargetLocal)
	if !errors.Is(err, memberclient.ErrStorageBackendMismatch) {
		t.Fatalf("Resolve() error = %v, want ErrStorageBackendMismatch — must refuse before returning the live store's data", err)
	}
}

// TestResolveLocal_EmptyStampNeverMismatches is the "predates this feature"
// rule: a run with no stamp at all must resolve normally against whatever
// the live backend has, not be treated as an automatic mismatch.
func TestResolveLocal_EmptyStampNeverMismatches(t *testing.T) {
	outputDir := t.TempDir()
	ls, err := storage.NewLocal(filepath.Join(outputDir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := ls.Put(ctx, "run-1/train/model/weights.bin", strings.NewReader("data"), -1); err != nil {
		t.Fatal(err)
	}

	r := &localResolver{
		outputDir:    outputDir,
		store:        ls,
		liveIdentity: "s3:new-bucket",
		runRepo:      &stubRunRepoWithStamp{storageBackend: ""},
	}
	resolved, err := r.Resolve(ctx, "pl", "train", "model", "run-1", TargetLocal)
	if err != nil {
		t.Fatalf("Resolve() with an unstamped run should not be treated as a mismatch: %v", err)
	}
	if resolved.LocalPath == "" {
		t.Fatal("Resolve() returned an empty LocalPath")
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
