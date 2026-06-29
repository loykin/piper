package srcfetch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piper/piper/internal/testutil"
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

// makeLocalRepo initialises a bare-minimum git repo with one committed file.
func makeLocalRepo(t *testing.T, fileName, content string) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", fileName)
	run("commit", "-m", "init")
	return dir
}

// TestGitFetcher_localRepo clones a file from a local git repository (no network).
func TestGitFetcher_localRepo(t *testing.T) {
	repoDir := makeLocalRepo(t, "train.py", "print('hello')\n")

	f := &GitFetcher{}
	destDir := t.TempDir()
	run := pipeline.Run{
		Source: "git",
		Repo:   "file://" + repoDir,
		Branch: "main",
		Path:   "train.py",
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

func TestGitFetcher_httpBasicAuth(t *testing.T) {
	repoURL := startAuthenticatedGitHTTPRepo(t, "train.py", "print('hello')\n", "git-user", "git-token")
	run := pipeline.Run{
		Source: "git",
		Repo:   repoURL,
		Branch: "main",
		Path:   "train.py",
	}

	_, err := (&GitFetcher{}).Fetch(context.Background(), run, t.TempDir())
	if err == nil {
		t.Fatal("expected auth error without git credentials")
	}

	f := &GitFetcher{cfg: Config{GitUser: "git-user", GitToken: "git-token"}}
	got, err := f.Fetch(context.Background(), run, t.TempDir())
	if err != nil {
		t.Fatalf("git fetch with basic auth failed: %v", err)
	}
	if body, err := os.ReadFile(got); err != nil || !strings.Contains(string(body), "hello") {
		t.Fatalf("fetched body = %q err=%v", string(body), err)
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
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(content)
	}))

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
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))

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
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

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

func startAuthenticatedGitHTTPRepo(t *testing.T, fileName, content, user, token string) string {
	t.Helper()
	repoDir := makeLocalRepo(t, fileName, content)
	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, "", "clone", "--bare", repoDir, bareDir)

	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotToken, ok := r.BasicAuth()
		if !ok || gotUser != user || gotToken != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		serveGitHTTPBackend(t, w, r, filepath.Dir(bareDir))
	}))
	return srv.URL + "/repo.git"
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func serveGitHTTPBackend(t *testing.T, w http.ResponseWriter, r *http.Request, projectRoot string) {
	t.Helper()
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "http-backend")
	cmd.Env = append(os.Environ(),
		"GIT_PROJECT_ROOT="+projectRoot,
		"GIT_HTTP_EXPORT_ALL=1",
		"REQUEST_METHOD="+r.Method,
		"PATH_INFO="+r.URL.Path,
		"QUERY_STRING="+r.URL.RawQuery,
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"CONTENT_LENGTH="+r.Header.Get("Content-Length"),
	)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git http-backend: %v", err)
	}
	header, payload, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		header, payload, ok = bytes.Cut(out, []byte("\n\n"))
	}
	if !ok {
		t.Fatalf("git http-backend returned malformed response: %q", string(out))
	}
	status := http.StatusOK
	for _, line := range bytes.Split(header, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		key := string(bytes.TrimSpace(name))
		val := string(bytes.TrimSpace(value))
		if strings.EqualFold(key, "Status") {
			if strings.HasPrefix(val, "403") {
				status = http.StatusForbidden
			} else if strings.HasPrefix(val, "404") {
				status = http.StatusNotFound
			}
			continue
		}
		w.Header().Add(key, val)
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
