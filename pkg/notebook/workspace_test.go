package notebook

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanWorkspacePathRejectsEscapes(t *testing.T) {
	for _, p := range []string{"../secret", "dir/../../secret", "/etc/passwd", `..\secret`} {
		if _, err := CleanWorkspacePath(p); err == nil {
			t.Fatalf("CleanWorkspacePath(%q) succeeded, want error", p)
		}
	}
	if got, err := CleanWorkspacePath("dir/./file.txt"); err != nil || got != "dir/file.txt" {
		t.Fatalf("CleanWorkspacePath normalized to %q, %v", got, err)
	}
}

func TestLocalWorkspaceReaderRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	reader := LocalWorkspaceReader{}
	vol := &NotebookVolume{WorkDir: root}
	if _, err := reader.Open(context.Background(), vol, "outside/secret.txt"); err == nil {
		t.Fatal("Open followed a symlink outside the workspace")
	}
	files, err := reader.ListFiles(context.Background(), vol, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Rel == "outside/secret.txt" {
			t.Fatal("ListFiles followed a symlink outside the workspace")
		}
	}
}

func TestLocalWorkspaceReaderOpenInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := (LocalWorkspaceReader{}).Open(context.Background(), &NotebookVolume{WorkDir: root}, "ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil || string(data) != "ok" {
		t.Fatalf("read = %q, %v", data, err)
	}
}
