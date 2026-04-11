package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

// ─── New factory ─────────────────────────────────────────────────────────────

func TestNew_local(t *testing.T) {
	run := pipeline.Run{Source: "local"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*LocalFetcher); !ok {
		t.Errorf("want *LocalFetcher, got %T", f)
	}
}

func TestNew_emptySource(t *testing.T) {
	// An empty source is treated as local
	run := pipeline.Run{Source: ""}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*LocalFetcher); !ok {
		t.Errorf("want *LocalFetcher, got %T", f)
	}
}

func TestNew_git(t *testing.T) {
	run := pipeline.Run{Source: "git"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*GitFetcher); !ok {
		t.Errorf("want *GitFetcher, got %T", f)
	}
}

func TestNew_s3(t *testing.T) {
	run := pipeline.Run{Source: "s3"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*S3Fetcher); !ok {
		t.Errorf("want *S3Fetcher, got %T", f)
	}
}

func TestNew_http(t *testing.T) {
	for _, src := range []string{"http", "https"} {
		run := pipeline.Run{Source: src}
		f, err := New(run, Config{})
		if err != nil {
			t.Fatalf("source=%q: %v", src, err)
		}
		if _, ok := f.(*HTTPFetcher); !ok {
			t.Errorf("source=%q: want *HTTPFetcher, got %T", src, f)
		}
	}
}

func TestNew_unknown(t *testing.T) {
	run := pipeline.Run{Source: "ftp"}
	_, err := New(run, Config{})
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

// ─── GitFetcher ───────────────────────────────────────────────────────────────

// TestGitFetcher_publicRepo clones a file from a real GitHub repository.
// Skipped when there is no network access.
func TestGitFetcher_publicRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	f := &GitFetcher{}
	destDir := t.TempDir()
	run := pipeline.Run{
		Source: "git",
		Repo:   "https://github.com/loykin/piper",
		Branch: "master",
		Path:   "examples/scripts/train.py",
	}

	got, err := f.Fetch(context.Background(), run, destDir)
	if err != nil {
		t.Fatalf("git fetch failed: %v", err)
	}

	if filepath.Base(got) != "train.py" {
		t.Errorf("want train.py, got %q", filepath.Base(got))
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("fetched file does not exist: %v", err)
	}
}

func TestGitFetcher_missingRepo(t *testing.T) {
	f := &GitFetcher{}
	run := pipeline.Run{Source: "git", Path: "foo.py"}
	_, err := f.Fetch(context.Background(), run, t.TempDir())
	if err == nil {
		t.Fatal("expected error when repo is empty")
	}
}

func TestGitFetcher_missingPath(t *testing.T) {
	f := &GitFetcher{}
	run := pipeline.Run{Source: "git", Repo: "https://github.com/loykin/piper"}
	_, err := f.Fetch(context.Background(), run, t.TempDir())
	if err == nil {
		t.Fatal("expected error when path is empty")
	}
}

// ─── LocalFetcher ─────────────────────────────────────────────────────────────

func TestLocalFetcher_withPath(t *testing.T) {
	f := &LocalFetcher{}
	destDir := t.TempDir()
	run := pipeline.Run{Path: "train.py"}

	got, err := f.Fetch(context.Background(), run, destDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(destDir, "train.py")
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestLocalFetcher_emptyPath(t *testing.T) {
	f := &LocalFetcher{}
	_, err := f.Fetch(context.Background(), pipeline.Run{Path: ""}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestLocalFetcher_nestedPath(t *testing.T) {
	f := &LocalFetcher{}
	got, err := f.Fetch(context.Background(), pipeline.Run{Path: "src/model/train.py"}, "/workdir")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workdir/src/model/train.py" {
		t.Errorf("unexpected path: %q", got)
	}
}

// ─── HTTPFetcher ──────────────────────────────────────────────────────────────

func TestHTTPFetcher_success(t *testing.T) {
	content := []byte("print('hello')")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	f := &HTTPFetcher{}
	destDir := t.TempDir()
	run := pipeline.Run{Source: "http", URL: srv.URL + "/scripts/train.py"}

	got, err := f.Fetch(context.Background(), run, destDir)
	if err != nil {
		t.Fatal(err)
	}

	// Filename must be the last segment of the URL
	if filepath.Base(got) != "train.py" {
		t.Errorf("want filename train.py, got %q", filepath.Base(got))
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: got %q", data)
	}
}

func TestHTTPFetcher_usePathWhenURLEmpty(t *testing.T) {
	content := []byte("# notebook")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	f := &HTTPFetcher{}
	// Should work when a URL is placed in the Path field instead of URL
	run := pipeline.Run{Source: "http", Path: srv.URL + "/nb.ipynb"}
	got, err := f.Fetch(context.Background(), run, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "nb.ipynb" {
		t.Errorf("want nb.ipynb, got %q", filepath.Base(got))
	}
}

func TestHTTPFetcher_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := &HTTPFetcher{}
	run := pipeline.Run{Source: "http", URL: srv.URL + "/missing.py"}
	_, err := f.Fetch(context.Background(), run, t.TempDir())
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestHTTPFetcher_emptyURL(t *testing.T) {
	f := &HTTPFetcher{}
	run := pipeline.Run{Source: "http"}
	_, err := f.Fetch(context.Background(), run, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}
