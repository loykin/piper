package commands

import (
	"path/filepath"
	"testing"

	piper "github.com/piper/piper"
	cliconfig "github.com/piper/piper/cmd/piper/config"
)

func TestEmbeddedPipelineWorkerConfigUsesResolvedStorageURL(t *testing.T) {
	dataDir := t.TempDir()
	p, err := piper.New(piper.Config{
		OutputDir: dataDir,
		Auth:      piper.AuthConfig{Trusted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	root := cliconfig.RootConfig{
		Server: cliconfig.ServerConfig{
			DataDir: dataDir,
		},
	}

	cfg := embeddedPipelineWorkerConfig(root, p, "http://localhost:8080", "local-pipeline", filepath.Join(dataDir, ".worker-state"), 1, "worker-token", "storage-token")
	want := "file://" + filepath.Join(dataDir, "store")
	if cfg.Store.StorageURL != want {
		t.Fatalf("StorageURL = %q, want resolved URL %q", cfg.Store.StorageURL, want)
	}
}
