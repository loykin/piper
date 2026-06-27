package commands

import (
	"path/filepath"
	"testing"

	cliconfig "github.com/piper/piper/cmd/piper/config"
)

func TestEmbeddedPipelineWorkerConfigDoesNotOwnStorage(t *testing.T) {
	dataDir := t.TempDir()
	root := cliconfig.RootConfig{
		Server: cliconfig.ServerConfig{
			DataDir: dataDir,
		},
	}

	cfg := embeddedPipelineWorkerConfig(root, "http://localhost:8080", "local-pipeline", filepath.Join(dataDir, ".worker-state"), 1, "worker-token")
	if cfg.Store.OutputDir != dataDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.Store.OutputDir, dataDir)
	}
}
