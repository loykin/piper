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
	// This test must include every top-level section defined in configFile.
	// When adding a new section to configFile, add a sample entry here too.
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
  agent: true
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
serving:
  model_dir: /data/models
  agent: true
log:
  format: json
notebook_worker:
  notebooks_root: /data/notebooks
  port_range: "8888-9900"
notebook_k8s:
  agent: true
  namespace: ml-notebooks
  worker_image: jupyter/minimal-notebook:latest
  storage_size: 10Gi
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStrictParseConfigFile_NotebookK8s(t *testing.T) {
	path := writeTempConfig(t, `
run:
  output_dir: /tmp/piper
notebook_k8s:
  agent: true
  namespace: ml-notebooks
  worker_image: jupyter/minimal-notebook:latest
  storage_class: standard
  storage_size: 10Gi
  pod_defaults:
    resources:
      cpu: "2"
      memory: 4Gi
    node_selector:
      accelerator: gpu
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error for notebook_k8s config: %v", err)
	}
}

func TestStrictParseConfigFile_NotebookWorker(t *testing.T) {
	path := writeTempConfig(t, `
notebook_worker:
  notebooks_root: /data/notebooks
  port_range: "8888-9900"
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error for notebook_worker config: %v", err)
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
	three := 3
	cf := configFile{
		Run: runSection{
			OutputDir:   "/data",
			Retries:     &three,
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
			Agent:                true,
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
		Serving: servingSection{
			ModelDir: "/data/models",
			Agent:    true,
		},
		NotebookK8s: notebookK8sSection{
			Agent:        true,
			Namespace:    "ml-notebooks",
			WorkerImage:  "jupyter/minimal-notebook:latest",
			StorageClass: "standard",
			StorageSize:  "10Gi",
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
	if !cfg.K8s.Agent {
		t.Error("K8s.Agent = false, want true")
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
	if cfg.Serving.ModelDir != "/data/models" {
		t.Errorf("Serving.ModelDir = %q, want /data/models", cfg.Serving.ModelDir)
	}
	if !cfg.Serving.Agent {
		t.Error("Serving.Agent = false, want true")
	}
	if !cfg.NotebookK8s.Agent {
		t.Error("NotebookK8s.Agent = false, want true")
	}
	if cfg.NotebookK8s.Namespace != "ml-notebooks" {
		t.Errorf("NotebookK8s.Namespace = %q, want ml-notebooks", cfg.NotebookK8s.Namespace)
	}
	if cfg.NotebookK8s.WorkerImage != "jupyter/minimal-notebook:latest" {
		t.Errorf("NotebookK8s.WorkerImage = %q, want jupyter/minimal-notebook:latest", cfg.NotebookK8s.WorkerImage)
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
