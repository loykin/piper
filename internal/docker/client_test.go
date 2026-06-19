package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewClientUsesDockerDesktopSocketWhenPresent(t *testing.T) {
	if os.Getenv("DOCKER_HOST") != "" {
		t.Skip("DOCKER_HOST is set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(home, ".docker", "run", "docker.sock")
	if info, err := os.Stat(socket); err != nil || info.IsDir() {
		t.Skip("Docker Desktop socket not present")
	}
	cli, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got, want := cli.DaemonHost(), "unix://"+socket; got != want {
		t.Fatalf("daemon host = %q, want %q", got, want)
	}
}
