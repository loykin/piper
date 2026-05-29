package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStrictParseConfigFile_ValidMinimal(t *testing.T) {
	path := writeTempConfig(t, `
run:
  output_dir: /tmp/piper
server:
  addr: :8080
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStrictParseConfigFile_ValidFull(t *testing.T) {
	path := writeTempConfig(t, `
run:
  output_dir: /tmp/piper
  retries: 3
  retry_delay: 10s
  concurrency: 8
source:
  s3:
    endpoint: minio:9000
    access_key: admin
    secret_key: password
    bucket: artifacts
    use_ssl: false
  git:
    token: ghp_abc
    user: bot
server:
  addr: :8080
  token: secret
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
k8s:
  agent_image: piper/piper:latest
  namespace: default
  in_cluster: true
  master_url: http://piper:8080
  default_image: alpine:3.20
  ttl_after_finished: 60
retention:
  run_ttl: 168h
  artifact_ttl: 720h
schedule:
  misfire_policy: skip
  misfire_grace_period: 1m
log:
  format: json
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStrictParseConfigFile_UnknownTopLevelKey(t *testing.T) {
	path := writeTempConfig(t, `
soource:
  s3:
    bucket: test
`)
	if err := StrictParseConfigFile(path); err == nil {
		t.Fatal("expected error for unknown top-level key 'soource', got nil")
	}
}

func TestStrictParseConfigFile_UnknownNestedKey(t *testing.T) {
	path := writeTempConfig(t, `
run:
  output_dir: /tmp
  unknown_field: oops
`)
	if err := StrictParseConfigFile(path); err == nil {
		t.Fatal("expected error for unknown nested key 'run.unknown_field', got nil")
	}
}

func TestStrictParseConfigFile_TypoInK8sKey(t *testing.T) {
	path := writeTempConfig(t, `
k8s:
  agent_imge: piper/piper:latest
`)
	if err := StrictParseConfigFile(path); err == nil {
		t.Fatal("expected error for typo 'agent_imge', got nil")
	}
}

func TestStrictParseConfigFile_NonExistentFile(t *testing.T) {
	if err := StrictParseConfigFile("/does/not/exist/piper.yaml"); err != nil {
		t.Fatalf("expected nil for missing file, got: %v", err)
	}
}

func TestConfigFileToConfig_MapsAllFields(t *testing.T) {
	cf := configFile{
		Run: runSection{
			OutputDir:   "/data",
			Retries:     3,
			RetryDelay:  5 * time.Second,
			Concurrency: 8,
		},
		Source: sourceSection{
			S3: s3Section{
				Endpoint:  "minio:9000",
				AccessKey: "admin",
				SecretKey: "password",
				Bucket:    "bucket",
				UseSSL:    true,
			},
			Git: gitSection{
				Token: "tok",
				User:  "bot",
			},
		},
		Server: serverSection{
			Addr:  ":9090",
			Token: "srv-token",
			TLS: tlsSection{
				Enabled:  true,
				CertFile: "/certs/tls.crt",
				KeyFile:  "/certs/tls.key",
			},
		},
		K8s: k8sSection{
			AgentImage:           "piper/piper:v1",
			AgentImagePullPolicy: "IfNotPresent",
			Namespace:            "ml",
			InCluster:            true,
			MasterURL:            "http://piper:8080",
			DefaultImage:         "python:3.12-slim",
			TTLAfterFinished:     120,
		},
		Retention: retentionSection{
			RunTTL:      168 * time.Hour,
			ArtifactTTL: 720 * time.Hour,
		},
		Schedule: scheduleSection{
			MisfirePolicy:      "run_once",
			MisfireGracePeriod: 2 * time.Minute,
		},
	}

	cfg := cf.toConfig()

	if cfg.OutputDir != "/data" {
		t.Errorf("OutputDir = %q, want /data", cfg.OutputDir)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryDelay != 5*time.Second {
		t.Errorf("RetryDelay = %v, want 5s", cfg.RetryDelay)
	}
	if cfg.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", cfg.Concurrency)
	}
	if cfg.S3.Endpoint != "minio:9000" {
		t.Errorf("S3.Endpoint = %q, want minio:9000", cfg.S3.Endpoint)
	}
	if cfg.S3.AccessKey != "admin" {
		t.Errorf("S3.AccessKey = %q, want admin", cfg.S3.AccessKey)
	}
	if !cfg.S3.UseSSL {
		t.Error("S3.UseSSL = false, want true")
	}
	if cfg.Git.Token != "tok" {
		t.Errorf("Git.Token = %q, want tok", cfg.Git.Token)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Server.Addr = %q, want :9090", cfg.Server.Addr)
	}
	if !cfg.Server.TLS.Enabled {
		t.Error("Server.TLS.Enabled = false, want true")
	}
	if cfg.Server.TLS.CertFile != "/certs/tls.crt" {
		t.Errorf("TLS.CertFile = %q, want /certs/tls.crt", cfg.Server.TLS.CertFile)
	}
	if cfg.K8s.AgentImage != "piper/piper:v1" {
		t.Errorf("K8s.AgentImage = %q, want piper/piper:v1", cfg.K8s.AgentImage)
	}
	if cfg.K8s.Namespace != "ml" {
		t.Errorf("K8s.Namespace = %q, want ml", cfg.K8s.Namespace)
	}
	if cfg.K8s.TTLAfterFinished != 120 {
		t.Errorf("K8s.TTLAfterFinished = %d, want 120", cfg.K8s.TTLAfterFinished)
	}
	if cfg.Retention.RunTTL != 168*time.Hour {
		t.Errorf("Retention.RunTTL = %v, want 168h", cfg.Retention.RunTTL)
	}
	if cfg.Schedule.MisfirePolicy != "run_once" {
		t.Errorf("Schedule.MisfirePolicy = %q, want run_once", cfg.Schedule.MisfirePolicy)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piper.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
