package config

import (
	"strings"
	"testing"
)

func TestValidateNotebookRejectsInvalidResourcesAndDuplicateGPUs(t *testing.T) {
	base := Defaults()
	base.Workers.Common.AgentAddr = "master:9090"
	base.Workers.Notebook.GPUs = []string{"0", "0"}
	if err := ValidateNotebook(base); err == nil || !strings.Contains(err.Error(), "gpus") {
		t.Fatalf("unexpected error: %v", err)
	}

	base.Workers.Notebook.GPUs = nil
	base.Workers.Notebook.Docker.Memory = "not-memory"
	if err := ValidateNotebook(base); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("unexpected error: %v", err)
	}

	base.Workers.Notebook.Docker.Memory = "1GiB"
	base.Workers.Notebook.Docker.CPUs = "0"
	if err := ValidateNotebook(base); err == nil || !strings.Contains(err.Error(), "cpus") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateNotebookRejectsInvalidVolume(t *testing.T) {
	cfg := Defaults()
	cfg.Workers.Common.AgentAddr = "master:9090"
	cfg.Workers.Notebook.Docker.Volumes = []NotebookDockerVolume{{Name: "data", HostPath: "relative", ContainerPath: "/data"}}
	if err := ValidateNotebook(cfg); err == nil || !strings.Contains(err.Error(), "host_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsStorageAndPullPolicy(t *testing.T) {
	cfg := Defaults()
	cfg.Workers.Common.MasterURL = "http://master:8080"
	cfg.Workers.Common.AgentAddr = "master:9090"
	cfg.Workers.K8s.Cluster = "test"
	cfg.Workers.K8s.Notebook.StorageSize = "invalid"
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "storage_size") {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg.Workers.K8s.Notebook.StorageSize = "10Gi"
	cfg.Workers.K8s.Pipeline.AgentImagePullPolicy = "Sometimes"
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "image_pull_policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRoleConfigsLoadFromFileOnly(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, `version: 1
workers:
  common:
    master_url: http://master:8080
    agent_addr: master:9090
  pipeline: {}
  notebook: {}
  serving: {}
  k8s:
    cluster: test
    in_cluster: true
`))
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, validate := range map[string]func(RootConfig) error{"pipeline": ValidatePipeline, "notebook": ValidateNotebook, "serving": ValidateServing, "k8s": ValidateK8s, "server": ValidateServer} {
		if err := validate(cfg); err != nil {
			t.Errorf("%s config-only validation: %v", name, err)
		}
	}
}
