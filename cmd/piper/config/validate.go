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
	DockerVolumes  []DockerVolume
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
		return fmt.Errorf("config: worker.capabilities.pipeline.concurrency must be at least 1")
	}
	return nil
}

func SelectPipeline(c RootConfig) (PipelineSelection, error) {
	infra, err := validateHostWorker(c.Worker, "pipeline")
	if err != nil {
		return PipelineSelection{}, err
	}
	out := PipelineSelection{Infrastructure: infra, Capability: *c.Worker.Capabilities.Pipeline}
	if infra == InfrastructureDocker {
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
	}
	defaultPipeline(&out.Capability)
	return out, nil
}

func ValidateNotebook(c RootConfig) error {
	selection, err := SelectNotebook(c)
	if err != nil {
		return err
	}
	if err := validatePortRange("worker.capabilities.notebook.port_range", selection.Capability.PortRange); err != nil {
		return err
	}
	if selection.Infrastructure == InfrastructureDocker {
		return validateDockerVolumes(selection.DockerVolumes)
	}
	return nil
}

func SelectNotebook(c RootConfig) (NotebookSelection, error) {
	infra, err := validateHostWorker(c.Worker, "notebook")
	if err != nil {
		return NotebookSelection{}, err
	}
	out := NotebookSelection{Infrastructure: infra, Capability: *c.Worker.Capabilities.Notebook}
	if infra == InfrastructureDocker {
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
		out.DockerVolumes = c.Worker.Docker.Volumes
	}
	defaultNotebook(&out.Capability)
	return out, nil
}

func ValidateServing(c RootConfig) error {
	_, err := SelectServing(c)
	return err
}

func SelectServing(c RootConfig) (ServingSelection, error) {
	infra, err := validateHostWorker(c.Worker, "serving")
	if err != nil {
		return ServingSelection{}, err
	}
	out := ServingSelection{Infrastructure: infra, Capability: *c.Worker.Capabilities.Serving}
	if infra == InfrastructureDocker {
		out.DockerNetwork = defaultString(c.Worker.Docker.Network, "bridge")
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
	if capabilityCount(c.Worker.Capabilities) == 0 {
		return fmt.Errorf("config: worker.capabilities must enable at least one of pipeline, notebook, or serving")
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
	if len(k.Namespaces) == 0 {
		return fmt.Errorf("config: worker.k8s.namespaces must contain at least one allowed namespace")
	}
	if err := unique("worker.k8s.namespaces", k.Namespaces); err != nil {
		return err
	}
	if p := c.Worker.Capabilities.Pipeline; p != nil && (p.Concurrency != 0 || p.OutputDir != "" || p.MetaDir != "" || p.Label != "") {
		return fmt.Errorf("config: worker.capabilities.pipeline is an activation marker for k8s workers")
	}
	if n := c.Worker.Capabilities.Notebook; n != nil && (n.NotebooksRoot != "" || n.PortRange != "") {
		return fmt.Errorf("config: worker.capabilities.notebook is an activation marker for k8s workers")
	}
	switch k.PipelineRunner.ImagePullPolicy {
	case "", "Always", "IfNotPresent", "Never":
	default:
		return fmt.Errorf("config: worker.k8s.pipeline_runner.image_pull_policy must be Always, IfNotPresent, or Never")
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

func validateHostWorker(w WorkerConfig, expected string) (string, error) {
	infra, err := validateSingleInfrastructure(w)
	if err != nil {
		return "", err
	}
	if infra == InfrastructureK8s {
		return "", fmt.Errorf("config: %s worker requires worker.baremetal or worker.docker", expected)
	}
	if capabilityCount(w.Capabilities) != 1 || !hasCapability(w.Capabilities, expected) {
		return "", fmt.Errorf("config: %s worker requires exactly the %s capability", expected, expected)
	}
	if infra == InfrastructureDocker && expected != "notebook" && len(w.Docker.Volumes) > 0 {
		return "", fmt.Errorf("config: worker.docker.volumes are only supported by the notebook capability")
	}
	return infra, nil
}

func validateSingleInfrastructure(w WorkerConfig) (string, error) {
	if w.MasterURL == "" {
		return "", fmt.Errorf("config: worker.master_url is required")
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

func capabilityCount(c WorkerCapabilitiesConfig) int {
	count := 0
	if c.Pipeline != nil {
		count++
	}
	if c.Notebook != nil {
		count++
	}
	if c.Serving != nil {
		count++
	}
	return count
}

func hasCapability(c WorkerCapabilitiesConfig, capability string) bool {
	switch capability {
	case "pipeline":
		return c.Pipeline != nil
	case "notebook":
		return c.Notebook != nil
	case "serving":
		return c.Serving != nil
	default:
		return false
	}
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

func validateDockerVolumes(volumes []DockerVolume) error {
	names := map[string]bool{}
	for _, v := range volumes {
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("config: worker.docker.volumes name is required")
		}
		if names[v.Name] {
			return fmt.Errorf("config: worker.docker.volumes contains duplicate name %q", v.Name)
		}
		names[v.Name] = true
		if !filepath.IsAbs(v.HostPath) {
			return fmt.Errorf("config: worker.docker.volumes[%s].host_path must be absolute", v.Name)
		}
		if !filepath.IsAbs(v.ContainerPath) {
			return fmt.Errorf("config: worker.docker.volumes[%s].container_path must be absolute", v.Name)
		}
	}
	return nil
}
