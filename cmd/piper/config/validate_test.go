package config

import (
	"strings"
	"testing"
)

func TestValidateNotebookRejectsDuplicateGPUs(t *testing.T) {
	base := Defaults()
	base.Workers.Common.MasterURL = "http://master:8080"
	base.Workers.Notebook.GPUs = []string{"0", "0"}
	if err := ValidateNotebook(base); err == nil || !strings.Contains(err.Error(), "gpus") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateNotebookRejectsInvalidVolume(t *testing.T) {
	cfg := Defaults()
	cfg.Workers.Common.MasterURL = "http://master:8080"
	cfg.Workers.Notebook.Docker.Volumes = []NotebookDockerVolume{{Name: "data", HostPath: "relative", ContainerPath: "/data"}}
	if err := ValidateNotebook(cfg); err == nil || !strings.Contains(err.Error(), "host_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsPullPolicy(t *testing.T) {
	cfg := Defaults()
	cfg.Workers.Common.MasterURL = "http://master:8080"
	cfg.Workers.K8s.Cluster = "test"
	cfg.Workers.K8s.Pipeline.RunnerImagePullPolicy = "Sometimes"
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "image_pull_policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRoleConfigsLoadFromFileOnly(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, `version: 2
workers:
  common:
    master_url: http://master:8080
  pipeline: {}
  notebook: {}
  serving: {}
  k8s:
    cluster: test
    namespaces: [test]
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
