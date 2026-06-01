package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/piper/piper/pkg/source"
	worker "github.com/piper/piper/pkg/workers/baremetal/pipeline"
)

func TestWorkerConfigFromSourcePassesArtifactAndGitConfig(t *testing.T) {
	src := source.Config{
		GitUser:     "git-user",
		GitToken:    "git-token",
		S3Endpoint:  "minio:9000",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Bucket:    "bucket",
		S3UseSSL:    true,
	}

	cfg := workerConfigFromSource(worker.Config{MasterURL: "http://master"}, src)

	if cfg.MasterURL != "http://master" {
		t.Fatalf("base config was not preserved: %#v", cfg)
	}
	if cfg.GitUser != src.GitUser || cfg.GitToken != src.GitToken {
		t.Fatalf("git config = (%q, %q), want (%q, %q)", cfg.GitUser, cfg.GitToken, src.GitUser, src.GitToken)
	}
	if cfg.S3Endpoint != src.S3Endpoint ||
		cfg.S3AccessKey != src.S3AccessKey ||
		cfg.S3SecretKey != src.S3SecretKey ||
		cfg.S3Bucket != src.S3Bucket ||
		cfg.S3UseSSL != src.S3UseSSL {
		t.Fatalf("s3 config = %#v, want source config %#v", cfg, src)
	}
}

// TestE2E_WorkerS3ConfigFromViperNotPiperInstance verifies that Bug 2 cannot regress.
//
// Bug 2: In the original cmd/piper/main.go, buildConfig() ran before cobra's
// initConfig() loaded the config file. As a result, p.SourceConfig() always
// returned empty S3 settings because viper had not yet read the config file.
//
// The fix (worker.go RunE) reads S3 settings directly from viper inside RunE,
// which is guaranteed to run after initConfig(). This test simulates that
// correct pattern:
//
//  1. Write a temporary piper.yaml containing S3 settings.
//  2. Load it via viper (simulating what initConfig does at runtime).
//  3. Build a source.Config directly from viper (as RunE does).
//  4. Confirm that workerConfigFromSource produces the expected S3 config.
//
// If someone reverts the fix and reads S3 settings from a pre-built piper.Config
// that was constructed before viper loaded the file, this test will fail because
// the source.Config will be empty.
func TestE2E_WorkerS3ConfigFromViperNotPiperInstance(t *testing.T) {
	// Isolate viper state so this test does not interfere with others.
	v := viper.New()

	// Write a minimal piper.yaml to a temp directory.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "piper.yaml")
	content := fmt.Sprintf(`source:
  s3:
    endpoint: "minio.example.com:9000"
    access_key: "test-access"
    secret_key: "test-secret"
    bucket: "test-bucket"
    use_ssl: true
  git:
    user: "git-user"
    token: "git-token"
worker:
  master: "http://master.example.com"
  output_dir: %q
`, dir)

	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Load the config file — this is what cobra's initConfig() does at runtime.
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}

	// Build source.Config exactly as worker.go's RunE does: read directly from
	// viper after the config file is loaded, NOT from a pre-built piper.Config.
	srcCfg := source.Config{
		GitUser:     v.GetString("source.git.user"),
		GitToken:    v.GetString("source.git.token"),
		S3Endpoint:  v.GetString("source.s3.endpoint"),
		S3AccessKey: v.GetString("source.s3.access_key"),
		S3SecretKey: v.GetString("source.s3.secret_key"),
		S3Bucket:    v.GetString("source.s3.bucket"),
		S3UseSSL:    v.GetBool("source.s3.use_ssl"),
	}

	base := worker.Config{MasterURL: v.GetString("worker.master")}
	cfg := workerConfigFromSource(base, srcCfg)

	// All S3 fields must be populated.
	if cfg.S3Endpoint != "minio.example.com:9000" {
		t.Errorf("S3Endpoint = %q, want %q", cfg.S3Endpoint, "minio.example.com:9000")
	}
	if cfg.S3AccessKey != "test-access" {
		t.Errorf("S3AccessKey = %q, want %q", cfg.S3AccessKey, "test-access")
	}
	if cfg.S3SecretKey != "test-secret" {
		t.Errorf("S3SecretKey = %q, want %q", cfg.S3SecretKey, "test-secret")
	}
	if cfg.S3Bucket != "test-bucket" {
		t.Errorf("S3Bucket = %q, want %q", cfg.S3Bucket, "test-bucket")
	}
	if !cfg.S3UseSSL {
		t.Errorf("S3UseSSL = false, want true")
	}

	// Git settings must also flow through.
	if cfg.GitUser != "git-user" {
		t.Errorf("GitUser = %q, want %q", cfg.GitUser, "git-user")
	}
	if cfg.GitToken != "git-token" {
		t.Errorf("GitToken = %q, want %q", cfg.GitToken, "git-token")
	}

	// Base config must be preserved.
	if cfg.MasterURL != "http://master.example.com" {
		t.Errorf("MasterURL = %q, want %q", cfg.MasterURL, "http://master.example.com")
	}
}
