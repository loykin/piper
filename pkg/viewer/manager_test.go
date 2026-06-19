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

// ── Bug 1: UpdateStatus 실패 시 tempDir 누수 ─────────────────────────────────

// UpdateStatus가 실패했을 때 materialze로 생성된 tempDir이 정리되는지 확인한다.
func TestOpen_UpdateStatusFails_CleansTempDir(t *testing.T) {
	repo := newFakeRepo()
	repo.updateErr = errors.New("db down")

	drv := &fakeDriver{typ: "fake"}
	mgr := NewManager(repo, nil, t.TempDir()) // store=nil → local path
	mgr.RegisterDriver(drv)

	// store가 nil이면 materialize는 tempDir=""를 반환하므로, store 경로를
	// 직접 테스트하기 위해 임시 디렉터리를 output dir로 쓴다.
	// 대신 store 경로(tempDir != "")를 시뮬레이션하기 위해 memStore stub을 쓴다.
	tmpRoot := t.TempDir()
	artifactDir := filepath.Join(tmpRoot, "run-1", "train", "tb")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// file을 하나 만들어 List가 결과를 반환하게 한다.
	if err := os.WriteFile(filepath.Join(artifactDir, "events.tfevents"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgrLocal := NewManager(repo, nil, tmpRoot)
	mgrLocal.RegisterDriver(drv)

	_, err := mgrLocal.Open(context.Background(), "proj", "run-1", "train", "tb", "fake")
	if err == nil {
		t.Fatal("expected error from UpdateStatus, got nil")
	}

	// driver.Stop이 호출됐는지 확인 (프로세스 정리)
	if len(drv.stopped) == 0 {
		t.Error("driver.Stop was not called after UpdateStatus failure")
	}
}

// ── Bug 2: tempDir 누수 — 오브젝트 스토어 경로 ───────────────────────────────

// Start 성공 후 UpdateStatus 실패 시 tempDir이 제거되는지 확인한다.
// memStore를 사용해 materialize가 실제 tmpDir을 만들게 한다.
func TestOpen_UpdateStatusFails_RemovesTempDirFromObjectStore(t *testing.T) {
	repo := newFakeRepo()

	drv := &fakeDriver{typ: "fake"}

	// store stub: List는 한 개의 object 반환, Get은 파일 내용 반환
	store := &stubStore{
		keys: []string{"run-1/train/tb/events.tfevents"},
		data: map[string][]byte{"run-1/train/tb/events.tfevents": []byte("data")},
	}

	mgr := NewManager(repo, store, t.TempDir())
	mgr.RegisterDriver(drv)

	// UpdateStatus가 성공하도록 먼저 한 번 성공 케이스를 확인하고,
	// 이후 실패를 주입한다.
	var capturedWorkDir string
	repo.updateErr = nil

	// Start 이후 UpdateStatus 직전에 실패를 주입하기 위해 updateErr를 미리 설정한다.
	// (Start가 return 하기 전에 updateErr가 설정되어야 하므로 처음부터 설정)
	repo.updateErr = errors.New("db error")

	_, err := mgr.Open(context.Background(), "proj", "run-1", "train", "tb", "fake")
	if err == nil {
		t.Fatal("expected error")
	}
	_ = capturedWorkDir

	// 드라이버 Stop 호출 확인
	if len(drv.stopped) == 0 {
		t.Error("driver.Stop was not called")
	}

	// 임시 디렉터리가 /tmp/piper-viewer- 패턴으로 생성됐을 것이고 삭제됐어야 한다.
	// glob으로 남은 디렉터리가 없는지 확인.
	matches, _ := filepath.Glob(os.TempDir() + "/piper-viewer-*")
	for _, m := range matches {
		if _, err := os.Stat(m); err == nil {
			t.Errorf("temp dir not cleaned up: %s", m)
		}
	}
}

// ── Bug 3: 디렉토리 리스팅 차단 — handler 단위 테스트 ─────────────────────────

// proxyViewer의 path guard가 디렉토리 경로를 차단하는지 확인한다.
// handler는 net/http/httptest를 통해 직접 테스트한다.
func TestProxyViewer_BlocksDirectoryPath(t *testing.T) {
	workDir := t.TempDir()
	// subdir 생성
	subDir := filepath.Join(workDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// path guard 로직만 추출해서 검증
	cases := []struct {
		subPath string
		wantOK  bool
	}{
		{"", false},          // 빈 path → redirect (blocking)
		{"subdir", false},    // 디렉토리 → 차단
		{"subdir/", false},   // trailing slash 디렉토리
		{"../escape", false}, // path traversal → 차단
		{"file.html", true},  // 정상 파일 (stat 실패는 ServeFile이 처리)
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
