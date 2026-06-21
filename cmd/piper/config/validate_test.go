package config

import (
	"strings"
	"testing"
)

func TestValidateNotebookRejectsDuplicateGPUs(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Baremetal: &BaremetalWorkerConfig{Capabilities: HostCapabilitiesConfig{
			Notebook: &NotebookCapabilityConfig{GPUs: []string{"0", "0"}, PortRange: "8888-9900"},
		}},
	}}
	if err := ValidateNotebook(cfg); err == nil || !strings.Contains(err.Error(), "gpus") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateNotebookRejectsInvalidDockerVolume(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Docker: &DockerWorkerConfig{Capabilities: DockerCapabilitiesConfig{
			Notebook: &DockerNotebookCapabilityConfig{PortRange: "8888-9900", Volumes: []NotebookDockerVolume{{Name: "data", HostPath: "relative", ContainerPath: "/data"}}},
		}},
	}}
	if err := ValidateNotebook(cfg); err == nil || !strings.Contains(err.Error(), "host_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkerRejectsMultipleInfrastructureTypes(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Baremetal: &BaremetalWorkerConfig{},
		Docker:    &DockerWorkerConfig{},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateHostWorkerRejectsMultipleCapabilities(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Docker: &DockerWorkerConfig{Capabilities: DockerCapabilitiesConfig{
			Pipeline: &PipelineCapabilityConfig{Concurrency: 1},
			Notebook: &DockerNotebookCapabilityConfig{PortRange: "8888-9900"},
		}},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsPullPolicy(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		K8s: &K8sWorkerConfig{
			Cluster: "test", Namespaces: []string{"test"}, InCluster: true,
			Capabilities: K8sCapabilitiesConfig{Pipeline: &K8sPipelineConfig{RunnerImagePullPolicy: "Sometimes"}},
		},
	}}
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "image_pull_policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRoleConfigsLoadFromFile(t *testing.T) {
	tests := map[string]struct {
		body     string
		validate func(RootConfig) error
	}{
		"pipeline": {`version: 3
worker:
  master_url: http://master:8080
  baremetal:
    capabilities:
      pipeline: {}
`, ValidatePipeline},
		"notebook": {`version: 3
worker:
  master_url: http://master:8080
  docker:
    network: bridge
    capabilities:
      notebook: {}
`, ValidateNotebook},
		"serving": {`version: 3
worker:
  master_url: http://master:8080
  baremetal:
    capabilities:
      serving: {}
`, ValidateServing},
		"k8s": {`version: 3
worker:
  master_url: http://master:8080
  k8s:
    cluster: test
    namespaces: [test]
    in_cluster: true
    capabilities:
      pipeline: {}
      notebook: {}
      serving: {}
`, ValidateK8s},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			loader := NewLoader()
			loader.SetConfigFile(writeConfig(t, tt.body))
			cfg, err := loader.Load()
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.validate(cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
}
