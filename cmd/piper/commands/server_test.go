package commands

import (
	"path/filepath"
	"testing"

	cliconfig "github.com/piper/piper/cmd/piper/config"
)

func TestServerCommandHasNoOperationalFlags(t *testing.T) {
	cmd := newServerCmd(cliconfig.NewLoader(), nil)
	if cmd.HasAvailableLocalFlags() {
		t.Fatalf("server command should be config-only, got flags:\n%s", cmd.LocalNonPersistentFlags().FlagUsages())
	}
}

func TestWorkerCommandHasNoOperationalFlags(t *testing.T) {
	cmd := newWorkerCmd(cliconfig.NewLoader())
	if cmd.HasAvailableLocalFlags() {
		t.Fatalf("worker command should be config-only, got flags:\n%s", cmd.LocalNonPersistentFlags().FlagUsages())
	}
}

func TestRunCommandHasNoOperationalFlags(t *testing.T) {
	cmd := newRunCmd(cliconfig.NewLoader(), nil)
	if cmd.HasAvailableLocalFlags() {
		t.Fatalf("run command should be config-only, got flags:\n%s", cmd.LocalNonPersistentFlags().FlagUsages())
	}
}

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
