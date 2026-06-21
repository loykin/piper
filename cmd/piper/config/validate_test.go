package config

import (
	"strings"
	"testing"
)

func TestValidateNotebookRejectsInvalidDockerVolume(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Docker:    &DockerWorkerConfig{Volumes: []DockerVolume{{Name: "data", HostPath: "relative", ContainerPath: "/data"}}},
		Capabilities: WorkerCapabilitiesConfig{
			Notebook: &NotebookCapabilityConfig{PortRange: "8888-9900"},
		},
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
		Capabilities: WorkerCapabilitiesConfig{
			Pipeline: &PipelineCapabilityConfig{},
		},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateHostWorkerRejectsMissingCapability(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Baremetal: &BaremetalWorkerConfig{},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "exactly the pipeline capability") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsMissingCapability(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		K8s: &K8sWorkerConfig{
			Cluster: "test", Namespaces: []string{"test"}, InCluster: true,
		},
	}}
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateHostWorkerRejectsMultipleCapabilities(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Docker:    &DockerWorkerConfig{},
		Capabilities: WorkerCapabilitiesConfig{
			Pipeline: &PipelineCapabilityConfig{Concurrency: 1},
			Notebook: &NotebookCapabilityConfig{PortRange: "8888-9900"},
		},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDockerVolumesOnlyForNotebook(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		Docker:    &DockerWorkerConfig{Volumes: []DockerVolume{{Name: "data", HostPath: "/tmp/data", ContainerPath: "/data"}}},
		Capabilities: WorkerCapabilitiesConfig{
			Pipeline: &PipelineCapabilityConfig{Concurrency: 1},
		},
	}}
	if err := ValidatePipeline(cfg); err == nil || !strings.Contains(err.Error(), "only supported by the notebook") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsPullPolicy(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		K8s: &K8sWorkerConfig{
			Cluster: "test", Namespaces: []string{"test"}, InCluster: true,
			PipelineRunner: K8sPipelineRunnerConfig{ImagePullPolicy: "Sometimes"},
		},
		Capabilities: WorkerCapabilitiesConfig{Pipeline: &PipelineCapabilityConfig{}},
	}}
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "image_pull_policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateK8sRejectsHostCapabilitySettings(t *testing.T) {
	cfg := RootConfig{Worker: WorkerConfig{
		MasterURL: "http://master:8080",
		K8s: &K8sWorkerConfig{
			Cluster: "test", Namespaces: []string{"test"}, InCluster: true,
		},
		Capabilities: WorkerCapabilitiesConfig{Pipeline: &PipelineCapabilityConfig{Concurrency: 2}},
	}}
	if err := ValidateK8s(cfg); err == nil || !strings.Contains(err.Error(), "activation marker") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRoleConfigsLoadFromFile(t *testing.T) {
	tests := map[string]struct {
		body     string
		validate func(RootConfig) error
	}{
		"pipeline": {`version: 4
worker:
  master_url: http://master:8080
  baremetal: {}
  capabilities:
    pipeline: {}
`, ValidatePipeline},
		"notebook": {`version: 4
worker:
  master_url: http://master:8080
  docker:
    network: bridge
  capabilities:
    notebook: {}
`, ValidateNotebook},
		"serving": {`version: 4
worker:
  master_url: http://master:8080
  baremetal: {}
  capabilities:
    serving: {}
`, ValidateServing},
		"k8s": {`version: 4
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
