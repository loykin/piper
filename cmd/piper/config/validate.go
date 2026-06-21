package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	InfrastructureBaremetal = "baremetal"
	InfrastructureDocker    = "docker"
	InfrastructureK8s       = "k8s"
)

type PipelineSelection struct {
	Infrastructure string
	Capability     PipelineCapabilityConfig
	DockerNetwork  string
}

type NotebookSelection struct {
	Infrastructure string
	Capability     NotebookCapabilityConfig
	DockerNetwork  string
	DockerVolumes  []NotebookDockerVolume
}

type ServingSelection struct {
	Infrastructure string
	Capability     ServingCapabilityConfig
	DockerNetwork  string
}

func ValidatePipeline(c RootConfig) error {
	selection, err := SelectPipeline(c)
	if err != nil {
		return err
	}
	if selection.Capability.Concurrency < 1 {
		return fmt.Errorf("config: worker.%s.capabilities.pipeline.concurrency must be at least 1", selection.Infrastructure)
	}
	return nil
}

func SelectPipeline(c RootConfig) (PipelineSelection, error) {
	infra, err := validateSingleInfrastructure(c.Worker)
	if err != nil {
		return PipelineSelection{}, err
	}
	if err := requireSingleHostCapability(c.Worker, "pipeline"); err != nil {
		return PipelineSelection{}, err
	}
	var out PipelineSelection
	out.Infrastructure = infra
	switch infra {
	case InfrastructureBaremetal:
		if c.Worker.Baremetal.Capabilities.Pipeline == nil {
			return out, capabilityError(infra, "pipeline")
		}
		out.Capability = *c.Worker.Baremetal.Capabilities.Pipeline
	case InfrastructureDocker:
		if c.Worker.Docker.Capabilities.Pipeline == nil {
			return out, capabilityError(infra, "pipeline")
		}
		out.Capability = *c.Worker.Docker.Capabilities.Pipeline
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
	default:
		return out, fmt.Errorf("config: pipeline worker requires worker.baremetal or worker.docker")
	}
	defaultPipeline(&out.Capability)
	return out, nil
}

func ValidateNotebook(c RootConfig) error {
	selection, err := SelectNotebook(c)
	if err != nil {
		return err
	}
	if err := validatePortRange("worker."+selection.Infrastructure+".capabilities.notebook.port_range", selection.Capability.PortRange); err != nil {
		return err
	}
	if err := unique("worker."+selection.Infrastructure+".capabilities.notebook.gpus", selection.Capability.GPUs); err != nil {
		return err
	}
	if selection.Infrastructure == InfrastructureDocker {
		return validateNotebookDocker(selection.DockerVolumes)
	}
	return nil
}

func SelectNotebook(c RootConfig) (NotebookSelection, error) {
	infra, err := validateSingleInfrastructure(c.Worker)
	if err != nil {
		return NotebookSelection{}, err
	}
	if err := requireSingleHostCapability(c.Worker, "notebook"); err != nil {
		return NotebookSelection{}, err
	}
	out := NotebookSelection{Infrastructure: infra}
	switch infra {
	case InfrastructureBaremetal:
		if c.Worker.Baremetal.Capabilities.Notebook == nil {
			return out, capabilityError(infra, "notebook")
		}
		out.Capability = *c.Worker.Baremetal.Capabilities.Notebook
	case InfrastructureDocker:
		cfg := c.Worker.Docker.Capabilities.Notebook
		if cfg == nil {
			return out, capabilityError(infra, "notebook")
		}
		out.Capability = NotebookCapabilityConfig{GPUs: cfg.GPUs, NotebooksRoot: cfg.NotebooksRoot, PortRange: cfg.PortRange}
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
		out.DockerVolumes = cfg.Volumes
	default:
		return out, fmt.Errorf("config: notebook worker requires worker.baremetal or worker.docker")
	}
	defaultNotebook(&out.Capability)
	return out, nil
}

func ValidateServing(c RootConfig) error {
	selection, err := SelectServing(c)
	if err != nil {
		return err
	}
	return unique("worker."+selection.Infrastructure+".capabilities.serving.gpus", selection.Capability.GPUs)
}

func SelectServing(c RootConfig) (ServingSelection, error) {
	infra, err := validateSingleInfrastructure(c.Worker)
	if err != nil {
		return ServingSelection{}, err
	}
	if err := requireSingleHostCapability(c.Worker, "serving"); err != nil {
		return ServingSelection{}, err
	}
	out := ServingSelection{Infrastructure: infra}
	switch infra {
	case InfrastructureBaremetal:
		if c.Worker.Baremetal.Capabilities.Serving == nil {
			return out, capabilityError(infra, "serving")
		}
		out.Capability = *c.Worker.Baremetal.Capabilities.Serving
	case InfrastructureDocker:
		if c.Worker.Docker.Capabilities.Serving == nil {
			return out, capabilityError(infra, "serving")
		}
		out.Capability = *c.Worker.Docker.Capabilities.Serving
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
	default:
		return out, fmt.Errorf("config: serving worker requires worker.baremetal or worker.docker")
	}
	return out, nil
}

func ValidateK8s(c RootConfig) error {
	infra, err := validateSingleInfrastructure(c.Worker)
	if err != nil {
		return err
	}
	if infra != InfrastructureK8s {
		return fmt.Errorf("config: k8s worker requires worker.k8s")
	}
	k := c.Worker.K8s
	if err := validateWorkerCommon(c.Worker); err != nil {
		return err
	}
	if k.Cluster == "" {
		return fmt.Errorf("config: worker.k8s.cluster is required")
	}
	if k.InCluster && k.Kubeconfig != "" {
		return fmt.Errorf("config: worker.k8s.in_cluster and kubeconfig are mutually exclusive")
	}
	if !k.InCluster && k.Kubeconfig == "" {
		return fmt.Errorf("config: worker.k8s.kubeconfig is required outside the cluster")
	}
	if k.Capabilities.Pipeline == nil && k.Capabilities.Notebook == nil && k.Capabilities.Serving == nil {
		return fmt.Errorf("config: worker.k8s.capabilities must enable at least one of pipeline, notebook, or serving")
	}
	if p := k.Capabilities.Pipeline; p != nil {
		switch p.RunnerImagePullPolicy {
		case "", "Always", "IfNotPresent", "Never":
		default:
			return fmt.Errorf("config: worker.k8s.capabilities.pipeline.runner_image_pull_policy must be Always, IfNotPresent, or Never")
		}
	}
	if len(k.Namespaces) == 0 {
		return fmt.Errorf("config: worker.k8s.namespaces must contain at least one allowed namespace")
	}
	return nil
}

func ValidateServer(c RootConfig) error {
	if c.Server.TLS.Enabled && (c.Server.TLS.CertFile == "" || c.Server.TLS.KeyFile == "") {
		return fmt.Errorf("config: server.tls requires cert_file and key_file")
	}
	if c.Server.DB.Driver != "" && c.Server.DB.Driver != "sqlite" && c.Server.DB.Driver != "postgres" {
		return fmt.Errorf("config: server.db.driver must be sqlite or postgres")
	}
	if c.Server.DB.Driver == "postgres" && c.Server.DB.DSN == "" {
		return fmt.Errorf("config: server.db.dsn is required for postgres")
	}
	if c.Server.Local.Enabled {
		if c.Server.Local.Concurrency < 1 {
			return fmt.Errorf("config: server.local.concurrency must be at least 1")
		}
		if err := validatePortRange("server.local.notebook_config.port_range", c.Server.Local.NotebookCfg.PortRange); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleInfrastructure(w WorkerConfig) (string, error) {
	if err := validateWorkerCommon(w); err != nil {
		return "", err
	}
	count, infra := 0, ""
	if w.Baremetal != nil {
		count++
		infra = InfrastructureBaremetal
	}
	if w.Docker != nil {
		count++
		infra = InfrastructureDocker
	}
	if w.K8s != nil {
		count++
		infra = InfrastructureK8s
	}
	if count != 1 {
		return "", fmt.Errorf("config: worker must configure exactly one of baremetal, docker, or k8s")
	}
	return infra, nil
}

func validateWorkerCommon(w WorkerConfig) error {
	if w.MasterURL == "" {
		return fmt.Errorf("config: worker.master_url is required")
	}
	return nil
}

func requireSingleHostCapability(w WorkerConfig, expected string) error {
	count, actual := 0, ""
	if w.Baremetal != nil {
		if w.Baremetal.Capabilities.Pipeline != nil {
			count++
			actual = "pipeline"
		}
		if w.Baremetal.Capabilities.Notebook != nil {
			count++
			actual = "notebook"
		}
		if w.Baremetal.Capabilities.Serving != nil {
			count++
			actual = "serving"
		}
	}
	if w.Docker != nil {
		if w.Docker.Capabilities.Pipeline != nil {
			count++
			actual = "pipeline"
		}
		if w.Docker.Capabilities.Notebook != nil {
			count++
			actual = "notebook"
		}
		if w.Docker.Capabilities.Serving != nil {
			count++
			actual = "serving"
		}
	}
	if count != 1 || actual != expected {
		return fmt.Errorf("config: %s worker requires exactly the %s capability", expected, expected)
	}
	return nil
}

func capabilityError(infra, capability string) error {
	return fmt.Errorf("config: worker.%s.capabilities.%s is required", infra, capability)
}

func defaultPipeline(c *PipelineCapabilityConfig) {
	if c.Concurrency == 0 {
		c.Concurrency = 4
	}
	if c.OutputDir == "" {
		c.OutputDir = "./piper-outputs"
	}
}

func defaultNotebook(c *NotebookCapabilityConfig) {
	if c.NotebooksRoot == "" {
		c.NotebooksRoot = "./notebooks"
	}
	if c.PortRange == "" {
		c.PortRange = "8888-9900"
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func validatePortRange(key, value string) error {
	p := strings.Split(value, "-")
	if len(p) != 2 {
		return fmt.Errorf("config: %s must be START-END", key)
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil || a < 1 || b > 65535 || a > b {
		return fmt.Errorf("config: %s is invalid", key)
	}
	return nil
}

func unique(key string, values []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return fmt.Errorf("config: %s contains duplicate %q", key, v)
		}
		seen[v] = true
	}
	return nil
}

func validateNotebookDocker(volumes []NotebookDockerVolume) error {
	names := map[string]bool{}
	for _, v := range volumes {
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("config: worker.docker.capabilities.notebook.volumes name is required")
		}
		if names[v.Name] {
			return fmt.Errorf("config: worker.docker.capabilities.notebook.volumes contains duplicate name %q", v.Name)
		}
		names[v.Name] = true
		if !filepath.IsAbs(v.HostPath) {
			return fmt.Errorf("config: worker.docker.capabilities.notebook.volumes[%s].host_path must be absolute", v.Name)
		}
		if !filepath.IsAbs(v.ContainerPath) {
			return fmt.Errorf("config: worker.docker.capabilities.notebook.volumes[%s].container_path must be absolute", v.Name)
		}
	}
	return nil
}
