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
db:
  path: /tmp/piper/custom.db
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigFileMapsSQLitePath(t *testing.T) {
	var cf configFile
	cf.DB.Path = "/tmp/piper/custom.db"
	cfg := cf.toConfig()
	if cfg.DBPath != cf.DB.Path {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, cf.DB.Path)
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
storage:
  disabled: true
  url: s3://artifact-bucket?endpoint=http://minio:9000&s3ForcePathStyle=true&accessKey=admin&secretKey=password
server:
  addr: :8080
  worker_token: worker-secret
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
retention:
  run_ttl: 168h
  artifact_ttl: 720h
schedule:
  misfire_policy: skip
  misfire_grace_period: 1m
serving:
  model_dir: /data/models
  worker: true
log:
  format: json
notebook_worker:
  mode: docker
  notebooks_root: /data/notebooks
  port_range: "8888-9900"
  docker:
    image: jupyter/scipy-notebook:latest
    network: bridge
    cpus: "2"
    memory: 4g
    shm_size: 1g
    read_only_root: true
    tmpfs:
      - /tmp
    user: "1000:100"
    volumes:
      - name: datasets
        host_path: /data/datasets
        container_path: /mnt/datasets
        read_only: true
    extra_args:
      - --ServerApp.disable_check_xsrf=true
notebook_k8s:
  worker: true
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
  worker: true
  namespace: ml-notebooks
  worker_image: jupyter/minimal-notebook:latest
  storage_class: standard
  storage_size: 10Gi
  pod_defaults:
    spec:
      nodeSelector:
        accelerator: gpu
      containers:
      - name: notebook
        resources:
          requests:
            cpu: "2"
            memory: 4Gi
`)
	if err := StrictParseConfigFile(path); err != nil {
		t.Fatalf("unexpected error for notebook_k8s config: %v", err)
	}
}

func TestStrictParseConfigFile_NotebookWorker(t *testing.T) {
	path := writeTempConfig(t, `
notebook_worker:
  mode: docker
  notebooks_root: /data/notebooks
  port_range: "8888-9900"
  docker:
    image: jupyter/scipy-notebook:latest
    network: bridge
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
		t.Fatal("expected error for unsupported k8s key 'agent_imge', got nil")
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
		Storage: storageSection{
			URL:      "s3://artifact-bucket?endpoint=http://minio:9000&s3ForcePathStyle=true&accessKey=admin&secretKey=password",
			Disabled: true,
			Token:    "store-token",
		},
		Server: serverSection{
			Addr:        ":9090",
			WorkerToken: "worker-token",
			TLS: tlsSection{
				Enabled:  true,
				CertFile: "/certs/tls.crt",
				KeyFile:  "/certs/tls.key",
			},
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
			Worker:   true,
		},
		NotebookWorker: notebookWorkerSection{
			NotebooksRoot: "/data/notebooks",
			PortRange:     "8888-9900",
			Mode:          "docker",
			Docker: notebookWorkerDockerSection{
				Image:        "jupyter/scipy-notebook:latest",
				Network:      "bridge",
				CPUs:         "2",
				Memory:       "4g",
				ShmSize:      "1g",
				ReadOnlyRoot: true,
				Tmpfs:        []string{"/tmp"},
				User:         "1000:100",
				Volumes: []notebookWorkerDockerVolume{{
					Name:          "datasets",
					HostPath:      "/data/datasets",
					ContainerPath: "/mnt/datasets",
					ReadOnly:      true,
				}},
				ExtraArgs: []string{"--ServerApp.disable_check_xsrf=true"},
			},
		},
		NotebookK8s: notebookK8sSection{
			Worker:       true,
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
	if !cfg.Storage.Disabled {
		t.Error("Storage.Disabled = false, want true")
	}
	if cfg.Storage.URL == "" {
		t.Error("Storage.URL = empty, want configured URL")
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
	if cfg.Retention.RunTTL != 168*time.Hour {
		t.Errorf("Retention.RunTTL = %v, want 168h", cfg.Retention.RunTTL)
	}
	if cfg.Schedule.MisfirePolicy != "run_once" {
		t.Errorf("Schedule.MisfirePolicy = %q, want run_once", cfg.Schedule.MisfirePolicy)
	}
	if cfg.Serving.ModelDir != "/data/models" {
		t.Errorf("Serving.ModelDir = %q, want /data/models", cfg.Serving.ModelDir)
	}
	if !cfg.Serving.Worker {
		t.Error("Serving.Worker = false, want true")
	}
	if cfg.NotebookWorker.Mode != "docker" {
		t.Errorf("NotebookWorker.Mode = %q, want docker", cfg.NotebookWorker.Mode)
	}
	if cfg.NotebookWorker.Docker.Image != "jupyter/scipy-notebook:latest" {
		t.Errorf("NotebookWorker.Docker.Image = %q, want jupyter/scipy-notebook:latest", cfg.NotebookWorker.Docker.Image)
	}
	if len(cfg.NotebookWorker.Docker.Volumes) != 1 || cfg.NotebookWorker.Docker.Volumes[0].Name != "datasets" {
		t.Fatalf("NotebookWorker.Docker.Volumes = %#v, want datasets", cfg.NotebookWorker.Docker.Volumes)
	}
	if len(cfg.NotebookWorker.Docker.ExtraArgs) != 1 {
		t.Fatalf("NotebookWorker.Docker.ExtraArgs = %#v", cfg.NotebookWorker.Docker.ExtraArgs)
	}
	if !cfg.NotebookK8s.Worker {
		t.Error("NotebookK8s.Worker = false, want true")
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
