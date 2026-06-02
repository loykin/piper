package blobstore_test

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/piper/piper/pkg/blobstore"
)

// runStoreTests runs the common Store contract tests against any implementation.
func runStoreTests(t *testing.T, st blobstore.Store) {
	t.Helper()
	ctx := context.Background()

	// Get on missing key → ErrNotFound
	t.Run("get_missing", func(t *testing.T) {
		_, err := st.Get(ctx, "no/such/key")
		if err != blobstore.ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	// Put then Get round-trips content
	t.Run("put_get", func(t *testing.T) {
		want := "hello blobstore"
		if err := st.Put(ctx, "a/b.txt", strings.NewReader(want), int64(len(want))); err != nil {
			t.Fatal(err)
		}
		rc, err := st.Get(ctx, "a/b.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		got, _ := io.ReadAll(rc)
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	// List returns keys under prefix
	t.Run("list", func(t *testing.T) {
		_ = st.Put(ctx, "prefix/x.txt", strings.NewReader("x"), 1)
		_ = st.Put(ctx, "prefix/y.txt", strings.NewReader("y"), 1)
		_ = st.Put(ctx, "other/z.txt", strings.NewReader("z"), 1)

		objs, err := st.List(ctx, "prefix/")
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		sort.Strings(keys)
		if len(keys) < 2 {
			t.Fatalf("expected at least 2 keys under prefix/, got %v", keys)
		}
		for _, k := range keys {
			if !strings.HasPrefix(k, "prefix/") {
				t.Errorf("unexpected key %q outside prefix", k)
			}
		}
	})

	// Delete removes the key
	t.Run("delete", func(t *testing.T) {
		key := "del/me.txt"
		_ = st.Put(ctx, key, strings.NewReader("bye"), 3)
		if err := st.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		_, err := st.Get(ctx, key)
		if err != blobstore.ErrNotFound {
			t.Fatalf("expected ErrNotFound after Delete, got %v", err)
		}
	})

	// Delete of non-existent key is a no-op
	t.Run("delete_missing", func(t *testing.T) {
		if err := st.Delete(ctx, "ghost/key"); err != nil {
			t.Fatalf("delete missing key should not error: %v", err)
		}
	})
}

// ─── MemStore ────────────────────────────────────────────────────────────────

func TestMemStore(t *testing.T) {
	runStoreTests(t, blobstore.NewMemStore())
}

func TestMemStore_URL(t *testing.T) {
	ms := blobstore.NewMemStore()
	if url, ok := ms.URL("any/key"); ok || url != "" {
		t.Errorf("MemStore.URL should return (\"\", false), got (%q, %v)", url, ok)
	}
}

func TestMemStore_Keys(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()
	_ = ms.Put(ctx, "k1", strings.NewReader("a"), 1)
	_ = ms.Put(ctx, "k2", strings.NewReader("b"), 1)
	keys := ms.Keys()
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "k1" || keys[1] != "k2" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

// ─── LocalStore ──────────────────────────────────────────────────────────────

func TestLocalStore(t *testing.T) {
	st, err := blobstore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runStoreTests(t, st)
}

func TestLocalStore_Root(t *testing.T) {
	dir := t.TempDir()
	st, err := blobstore.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	if st.Root() != abs {
		t.Errorf("Root()=%q, want %q", st.Root(), abs)
	}
}

func TestLocalStore_URL(t *testing.T) {
	st, _ := blobstore.NewLocal(t.TempDir())
	if _, ok := st.URL("k"); ok {
		t.Error("LocalStore.URL should return false")
	}
}

func TestLocalStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "deep", "dir")
	st, err := blobstore.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.Root()); err != nil {
		t.Errorf("directory was not created: %v", err)
	}
}

// ─── Open factory ────────────────────────────────────────────────────────────

func TestOpen_file_scheme(t *testing.T) {
	dir := t.TempDir()
	st, err := blobstore.Open("file://"+dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.(*blobstore.LocalStore); !ok {
		t.Errorf("expected *LocalStore, got %T", st)
	}
}

func TestOpen_unsupported_scheme(t *testing.T) {
	_, err := blobstore.Open("ftp://example.com/bucket", "")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestOpen_invalid_url(t *testing.T) {
	_, err := blobstore.Open("://broken", "")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// ─── UploadPath / UploadDir / DownloadDir helpers ────────────────────────────

func TestUploadPath_file(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()

	// write a temp file
	dir := t.TempDir()
	fpath := filepath.Join(dir, "data.txt")
	_ = os.WriteFile(fpath, []byte("content"), 0644)

	if err := blobstore.UploadPath(ctx, ms, fpath, "run/step/out"); err != nil {
		t.Fatal(err)
	}
	keys := ms.Keys()
	if len(keys) != 1 || keys[0] != "run/step/out/data.txt" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

func TestUploadPath_directory(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0755)
	_ = os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0644)

	if err := blobstore.UploadPath(ctx, ms, dir, "run/step/out"); err != nil {
		t.Fatal(err)
	}
	keys := ms.Keys()
	sort.Strings(keys)
	want := []string{"run/step/out/a.txt", "run/step/out/sub/b.txt"}
	if len(keys) != len(want) {
		t.Fatalf("got keys %v, want %v", keys, want)
	}
	for i, k := range keys {
		if k != want[i] {
			t.Errorf("keys[%d]=%q, want %q", i, k, want[i])
		}
	}
}

func TestDownloadDir(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()
	_ = ms.Put(ctx, "prefix/foo.txt", strings.NewReader("foo"), 3)
	_ = ms.Put(ctx, "prefix/sub/bar.txt", strings.NewReader("bar"), 3)

	destDir := t.TempDir()
	if err := blobstore.DownloadDir(ctx, ms, "prefix/", destDir); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(filepath.Join(destDir, "foo.txt"))
	if string(got) != "foo" {
		t.Errorf("foo.txt: got %q", got)
	}
	got, _ = os.ReadFile(filepath.Join(destDir, "sub", "bar.txt"))
	if string(got) != "bar" {
		t.Errorf("sub/bar.txt: got %q", got)
	}
}

func TestDownloadDir_empty_prefix_error(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()
	if err := blobstore.DownloadDir(ctx, ms, "noexist/", t.TempDir()); err == nil {
		t.Fatal("expected error when prefix has no objects")
	}
}

// ─── ServeHTTP helper ────────────────────────────────────────────────────────

func TestServeHTTP(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()
	want := "streamed content"
	_ = ms.Put(ctx, "my/key", strings.NewReader(want), int64(len(want)))

	rr := httptest.NewRecorder()
	if err := blobstore.ServeHTTP(ctx, ms, "my/key", rr); err != nil {
		t.Fatal(err)
	}
	if rr.Body.String() != want {
		t.Errorf("got %q, want %q", rr.Body.String(), want)
	}
}

func TestServeHTTP_missing(t *testing.T) {
	ctx := context.Background()
	ms := blobstore.NewMemStore()
	err := blobstore.ServeHTTP(ctx, ms, "no/such", httptest.NewRecorder())
	if err != blobstore.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
