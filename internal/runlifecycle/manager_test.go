package runlifecycle

import (
	"path/filepath"
	"testing"
)

func TestRunWorkspaceDir(t *testing.T) {
	got := runWorkspaceDir("/data/piper-outputs", "run-123")
	want := filepath.Join("/data/piper-outputs", "run-123")
	if got != want {
		t.Fatalf("runWorkspaceDir() = %q, want %q", got, want)
	}
}
