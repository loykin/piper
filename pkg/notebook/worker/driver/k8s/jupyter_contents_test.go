package notebookworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeJupyterServer returns a test server that mimics the Jupyter Contents API.
type fakeJupyterServer struct {
	srv   *httptest.Server
	token string
	tree  map[string]jupyterItem // path → item (including content)
}

func newFakeJupyterServer(t *testing.T, token string, tree map[string]jupyterItem) *fakeJupyterServer {
	t.Helper()
	f := &fakeJupyterServer{token: token, tree: tree}
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.handler)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJupyterServer) handler(w http.ResponseWriter, r *http.Request) {
	if f.token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "token "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	// Extract virtual path from URL: /proxy/api/contents/<path>
	const prefix = "/api/contents"
	virtualPath := ""
	if idx := strings.Index(r.URL.Path, prefix); idx >= 0 {
		virtualPath = strings.TrimPrefix(r.URL.Path[idx:], prefix)
		virtualPath = strings.TrimPrefix(virtualPath, "/")
	}

	item, ok := f.tree[virtualPath]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(item)
}

func (f *fakeJupyterServer) hostPort() string {
	return strings.TrimPrefix(f.srv.URL, "http://")
}

// singleDirTree builds a tree with one root directory containing the given files.
func singleDirTree(files ...string) map[string]jupyterItem {
	root := jupyterItem{Name: "work", Path: "", Type: "directory"}
	for _, name := range files {
		ext := ""
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			ext = name[idx:]
		}
		typ := "file"
		if ext == ".ipynb" {
			typ = "notebook"
		}
		root.Content = append(root.Content, jupyterItem{Name: name, Path: name, Type: typ})
	}
	return map[string]jupyterItem{"": root}
}

func TestJupyterContents_RootListing(t *testing.T) {
	fs := newFakeJupyterServer(t, "tok", singleDirTree("main.py", "train.ipynb"))
	client := newJupyterContentsClient()

	files, truncated, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "tok", "", nil, 500)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %v", files)
	}
}

func TestJupyterContents_TokenHeader(t *testing.T) {
	fs := newFakeJupyterServer(t, "secret-token", singleDirTree("a.py"))
	client := newJupyterContentsClient()

	// Wrong token → error.
	_, _, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "wrong", "", nil, 500)
	if err == nil {
		t.Fatal("expected error with wrong token, got nil")
	}

	// Correct token → success.
	files, _, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "secret-token", "", nil, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %v", files)
	}
}

func TestJupyterContents_ExtensionFilter(t *testing.T) {
	fs := newFakeJupyterServer(t, "", singleDirTree("main.py", "train.ipynb", "README.md"))
	client := newJupyterContentsClient()

	files, _, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "", "", []string{".ipynb"}, 500)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if len(files) != 1 || files[0] != "train.ipynb" {
		t.Fatalf("expected [train.ipynb], got %v", files)
	}
}

func TestJupyterContents_NestedDirectoryRecursion(t *testing.T) {
	tree := map[string]jupyterItem{
		"": {
			Name: "work", Path: "", Type: "directory",
			Content: []jupyterItem{
				{Name: "main.py", Path: "main.py", Type: "file"},
				{Name: "src", Path: "src", Type: "directory"},
			},
		},
		"src": {
			Name: "src", Path: "src", Type: "directory",
			Content: []jupyterItem{
				{Name: "model.py", Path: "src/model.py", Type: "file"},
			},
		},
	}
	fs := newFakeJupyterServer(t, "", tree)
	client := newJupyterContentsClient()

	files, _, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "", "", nil, 500)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %v", files)
	}
}

func TestJupyterContents_MaxFileTruncation(t *testing.T) {
	fs := newFakeJupyterServer(t, "", singleDirTree("a.py", "b.py", "c.py"))
	client := newJupyterContentsClient()

	files, truncated, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "", "", nil, 2)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
}

func TestJupyterContents_ContextCancellation(t *testing.T) {
	fs := newFakeJupyterServer(t, "", singleDirTree("a.py"))
	client := newJupyterContentsClient()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, _, err := client.ListFiles(ctx, fs.hostPort(), "/", "", "", nil, 500)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestJupyterContents_HiddenDirectorySkip(t *testing.T) {
	tree := map[string]jupyterItem{
		"": {
			Name: "work", Path: "", Type: "directory",
			Content: []jupyterItem{
				{Name: "visible.py", Path: "visible.py", Type: "file"},
				{Name: ".hidden", Path: ".hidden", Type: "directory"},
			},
		},
		".hidden": {
			Name: ".hidden", Path: ".hidden", Type: "directory",
			Content: []jupyterItem{
				{Name: "secret.py", Path: ".hidden/secret.py", Type: "file"},
			},
		},
	}
	fs := newFakeJupyterServer(t, "", tree)
	client := newJupyterContentsClient()

	files, _, err := client.ListFiles(context.Background(), fs.hostPort(), "/", "", "", nil, 500)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if len(files) != 1 || files[0] != "visible.py" {
		t.Fatalf("expected [visible.py], got %v", files)
	}
}
