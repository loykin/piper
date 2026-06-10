package notebook

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestWalkFiles_ExtensionFilter(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "main.py")
	touch(t, dir, "train.ipynb")
	touch(t, dir, "README.md")

	files, truncated := WalkFiles(dir, []string{".py", ".ipynb"}, 500)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	want := []string{"main.py", "train.ipynb"}
	sort.Strings(want)
	if !equalSlice(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestWalkFiles_NoFilter(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.py")
	touch(t, dir, "b.md")

	files, _ := WalkFiles(dir, nil, 500)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestWalkFiles_HiddenDirectorySkip(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, ".git")
	if err := os.MkdirAll(hidden, 0755); err != nil {
		t.Fatal(err)
	}
	touch(t, hidden, "config")
	touch(t, dir, "visible.py")

	files, _ := WalkFiles(dir, nil, 500)
	if len(files) != 1 || files[0] != "visible.py" {
		t.Fatalf("files = %v, want [visible.py]", files)
	}
}

func TestWalkFiles_MaxFileTruncation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.py", "b.py", "c.py"} {
		touch(t, dir, name)
	}

	files, truncated := WalkFiles(dir, nil, 2)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
}

func TestWalkFiles_SortedAscending(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "z.py")
	touch(t, dir, "a.py")
	touch(t, dir, "m.py")

	files, _ := WalkFiles(dir, nil, 500)
	if !sort.StringsAreSorted(files) {
		t.Fatalf("files not sorted: %v", files)
	}
}

func TestWalkFiles_NestedDirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "models")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	touch(t, sub, "model.py")
	touch(t, dir, "main.py")

	files, _ := WalkFiles(dir, []string{".py"}, 500)
	if len(files) != 2 {
		t.Fatalf("expected 2, got %v", files)
	}
}

func TestWalkFiles_SlashSeparator(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	touch(t, sub, "main.py")

	files, _ := WalkFiles(dir, nil, 500)
	for _, f := range files {
		for _, c := range f {
			if c == '\\' {
				t.Fatalf("path contains backslash: %q", f)
			}
		}
	}
}

func TestWalkFiles_EmptyRoot(t *testing.T) {
	dir := t.TempDir()
	files, truncated := WalkFiles(dir, nil, 500)
	if len(files) != 0 {
		t.Fatalf("expected empty, got %v", files)
	}
	if truncated {
		t.Fatal("unexpected truncation on empty dir")
	}
}

func TestResponseHelpers(t *testing.T) {
	r := ReadyResponse([]string{"a.py"}, false)
	if r.State != FSAccessReady {
		t.Fatalf("state = %s", r.State)
	}

	tr := TransitioningResponse("pod pending")
	if tr.State != FSAccessTransitioning {
		t.Fatalf("state = %s", tr.State)
	}
	if tr.RetryAfterSeconds == 0 {
		t.Fatal("expected non-zero retry_after_seconds")
	}

	u := UnavailableResponse("PVC lost")
	if u.State != FSAccessUnavailable {
		t.Fatalf("state = %s", u.State)
	}
}

// helpers

func touch(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
