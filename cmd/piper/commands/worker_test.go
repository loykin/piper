package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/piper/piper/pkg/source"
	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
)

func TestWorkerConfigFromSource_GitPassthrough(t *testing.T) {
	src := source.Config{
		GitUser:  "git-user",
		GitToken: "git-token",
	}

	cfg := workerConfigFromSource(worker.Config{MasterURL: "http://master"}, src)

	if cfg.MasterURL != "http://master" {
		t.Fatalf("base config was not preserved: MasterURL=%q", cfg.MasterURL)
	}
	if cfg.GitUser != src.GitUser || cfg.GitToken != src.GitToken {
		t.Fatalf("git config = (%q, %q), want (%q, %q)", cfg.GitUser, cfg.GitToken, src.GitUser, src.GitToken)
	}
}

func TestWorkerConfigFromSource_StorageURLPassthrough(t *testing.T) {
	url := "s3://my-bucket?region=us-east-1&accessKey=ak&secretKey=sk"
	src := source.Config{StorageURL: url}

	cfg := workerConfigFromSource(worker.Config{}, src)

	if cfg.StorageURL != url {
		t.Fatalf("StorageURL = %q, want %q", cfg.StorageURL, url)
	}
}

func TestWorkerConfigFromSource_PreservesExistingStorageURL(t *testing.T) {
	existing := "s3://existing-bucket?region=eu-west-1"
	src := source.Config{StorageURL: "s3://other-bucket"}

	cfg := workerConfigFromSource(worker.Config{StorageURL: existing}, src)

	if cfg.StorageURL != existing {
		t.Errorf("existing StorageURL should not be overridden; got %q", cfg.StorageURL)
	}
}

func TestWorkerConfigFromSource_NilStorageURL(t *testing.T) {
	src := source.Config{GitUser: "user"}

	cfg := workerConfigFromSource(worker.Config{}, src)

	if cfg.StorageURL != "" {
		t.Errorf("StorageURL should be empty when source has none, got %q", cfg.StorageURL)
	}
}

// TestE2E_WorkerStorageURLFromViper verifies that the worker correctly reads
// StorageURL from viper config (Bug 2 regression prevention).
func TestE2E_WorkerStorageURLFromViper(t *testing.T) {
	v := viper.New()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piper.yaml")
	storageURL := "s3://test-bucket?region=us-east-1&endpoint=https://minio.example.com:9000&accessKey=test-access&secretKey=test-secret&s3ForcePathStyle=true"
	content := fmt.Sprintf(`storage:
  url: %q
source:
  git:
    user: "git-user"
    token: "git-token"
worker:
  master: "http://master.example.com"
  output_dir: %q
`, storageURL, dir)

	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}

	srcCfg := source.Config{
		GitUser:    v.GetString("source.git.user"),
		GitToken:   v.GetString("source.git.token"),
		StorageURL: v.GetString("storage.url"),
	}

	base := worker.Config{MasterURL: v.GetString("worker.master")}
	cfg := workerConfigFromSource(base, srcCfg)

	if !strings.Contains(cfg.StorageURL, "test-bucket") {
		t.Errorf("StorageURL = %q, want it to contain test-bucket", cfg.StorageURL)
	}
	if cfg.GitUser != "git-user" {
		t.Errorf("GitUser = %q, want git-user", cfg.GitUser)
	}
	if cfg.MasterURL != "http://master.example.com" {
		t.Errorf("MasterURL = %q, want http://master.example.com", cfg.MasterURL)
	}
}
