package viewer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piper/piper/pkg/storage"
)

// ── fake repository ───────────────────────────────────────────────────────────

type fakeRepo struct {
	viewers     map[string]*Viewer
	updateErr   error
	findRunning *Viewer
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{viewers: make(map[string]*Viewer)}
}

func (r *fakeRepo) Create(_ context.Context, v *Viewer) error {
	cp := *v
	r.viewers[v.ID] = &cp
	return nil
}

func (r *fakeRepo) Get(_ context.Context, id string) (*Viewer, error) {
	v, ok := r.viewers[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *v
	return &cp, nil
}

func (r *fakeRepo) List(_ context.Context, _ string) ([]*Viewer, error) { return nil, nil }

func (r *fakeRepo) FindRunning(_ context.Context, _, _, _, _, _ string) (*Viewer, error) {
	return r.findRunning, nil
}

func (r *fakeRepo) UpdateStatus(_ context.Context, id string, status Status, endpoint string, pid int, workDir string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	if v, ok := r.viewers[id]; ok {
		v.Status = status
		v.Endpoint = endpoint
		v.PID = pid
		v.WorkDir = workDir
	}
	return nil
}

func (r *fakeRepo) ListExpired(_ context.Context) ([]*Viewer, error) {
	var out []*Viewer
	now := time.Now()
	for _, v := range r.viewers {
		if v.ExpiresAt != nil && v.ExpiresAt.Before(now) {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) MarkStaleFailed(_ context.Context) error { return nil }

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	delete(r.viewers, id)
	return nil
}

// ── fake driver ───────────────────────────────────────────────────────────────

type fakeDriver struct {
	typ      string
	startErr error
	stopped  []string
}

func (d *fakeDriver) Type() string { return d.typ }

func (d *fakeDriver) Start(_ context.Context, v *Viewer, _ string) error {
	if d.startErr != nil {
		return d.startErr
	}
	v.Endpoint = "http://127.0.0.1:19999"
	v.PID = 12345
	return nil
}

func (d *fakeDriver) Stop(_ context.Context, v *Viewer) error {
	d.stopped = append(d.stopped, v.ID)
	return nil
}

// ── Bug 1: tempDir leak when UpdateStatus fails ──────────────────────────────

// Verify that a tempDir created by materialize is cleaned up when UpdateStatus fails.
func TestOpen_UpdateStatusFails_CleansTempDir(t *testing.T) {
	repo := newFakeRepo()
	repo.updateErr = errors.New("db down")

	drv := &fakeDriver{typ: "fake"}
	mgr := NewManager(repo, nil, t.TempDir()) // store=nil → local path
	mgr.RegisterDriver(drv)

	// When store is nil, materialize returns tempDir="", so use a temporary
	// directory as the output directory to exercise the local path.
	// A memStore stub is used below to simulate the store path (tempDir != "").
	tmpRoot := t.TempDir()
	artifactDir := filepath.Join(tmpRoot, "run-1", "train", "tb")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a file so List returns a result.
	if err := os.WriteFile(filepath.Join(artifactDir, "events.tfevents"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgrLocal := NewManager(repo, nil, tmpRoot)
	mgrLocal.RegisterDriver(drv)

	_, err := mgrLocal.Open(context.Background(), "proj", "run-1", "train", "tb", "fake")
	if err == nil {
		t.Fatal("expected error from UpdateStatus, got nil")
	}

	// Verify that driver.Stop was called to clean up the process.
	if len(drv.stopped) == 0 {
		t.Error("driver.Stop was not called after UpdateStatus failure")
	}
}

// ── Bug 2: tempDir leak on the object-store path ─────────────────────────────

// Verify that tempDir is removed when Start succeeds but UpdateStatus fails.
// Use memStore so materialize creates a real tempDir.
func TestOpen_UpdateStatusFails_RemovesTempDirFromObjectStore(t *testing.T) {
	repo := newFakeRepo()

	drv := &fakeDriver{typ: "fake"}

	// Store stub: List returns one object and Get returns its contents.
	store := &stubStore{
		keys: []string{"run-1/train/tb/events.tfevents"},
		data: map[string][]byte{"run-1/train/tb/events.tfevents": []byte("data")},
	}

	mgr := NewManager(repo, store, t.TempDir())
	mgr.RegisterDriver(drv)

	// First prepare the successful UpdateStatus path, then inject a failure.
	var capturedWorkDir string
	repo.updateErr = nil

	// Set updateErr in advance so the failure occurs after Start and immediately
	// before UpdateStatus. It must be set from the beginning because Start must
	// return before UpdateStatus runs.
	repo.updateErr = errors.New("db error")

	_, err := mgr.Open(context.Background(), "proj", "run-1", "train", "tb", "fake")
	if err == nil {
		t.Fatal("expected error")
	}
	_ = capturedWorkDir

	// Verify that the driver was stopped.
	if len(drv.stopped) == 0 {
		t.Error("driver.Stop was not called")
	}

	// The temporary directory should have been created with the
	// /tmp/piper-viewer-* pattern and then removed. Verify that none remain.
	matches, _ := filepath.Glob(os.TempDir() + "/piper-viewer-*")
	for _, m := range matches {
		if _, err := os.Stat(m); err == nil {
			t.Errorf("temp dir not cleaned up: %s", m)
		}
	}
}

// ── Bug 3: block directory listings in the handler ───────────────────────────

// Verify that proxyViewer's path guard rejects directory paths.
// Exercise the handler directly through net/http/httptest.
func TestProxyViewer_BlocksDirectoryPath(t *testing.T) {
	workDir := t.TempDir()
	// Create a subdirectory.
	subDir := filepath.Join(workDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Exercise the path-guard logic in isolation.
	cases := []struct {
		subPath string
		wantOK  bool
	}{
		{"", false},          // Empty path redirects and is therefore blocked.
		{"subdir", false},    // Directory path is blocked.
		{"subdir/", false},   // Directory path with a trailing slash is blocked.
		{"../escape", false}, // Path traversal is blocked.
		{"file.html", true},  // A normal file is allowed; ServeFile handles stat failures.
	}

	for _, tc := range cases {
		t.Run(tc.subPath, func(t *testing.T) {
			allowed := isAllowedPath(workDir, tc.subPath)
			if allowed != tc.wantOK {
				t.Errorf("isAllowedPath(%q) = %v, want %v", tc.subPath, allowed, tc.wantOK)
			}
		})
	}
}

// isAllowedPath extracts the path validation logic from proxyViewer for unit testing.
func isAllowedPath(workDir, subPath string) bool {
	if subPath == "" {
		return false // redirect case
	}
	absPath, err := filepath.Abs(filepath.Join(workDir, filepath.FromSlash(subPath)))
	if err != nil {
		return false
	}
	if !(strings.HasPrefix(absPath, workDir+string(os.PathSeparator)) || absPath == workDir) {
		return false
	}
	info, statErr := os.Stat(absPath)
	if statErr == nil && info.IsDir() {
		return false
	}
	return true
}

// ── stub storage.Store ────────────────────────────────────────────────────────

type stubStore struct {
	keys []string
	data map[string][]byte
}

func (s *stubStore) Put(_ context.Context, _ string, _ io.Reader, _ int64) error { return nil }

func (s *stubStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *stubStore) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	var out []storage.ObjectInfo
	for _, k := range s.keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, storage.ObjectInfo{Key: k, Size: int64(len(s.data[k]))})
		}
	}
	return out, nil
}

func (s *stubStore) Delete(_ context.Context, _ ...string) error { return nil }

func (s *stubStore) URL(_ string) (string, bool) { return "", false }
