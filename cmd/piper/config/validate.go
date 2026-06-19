package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/go-units"
	"k8s.io/apimachinery/pkg/api/resource"
)

func ValidatePipeline(c RootConfig) error {
	if c.Workers.Common.AgentAddr == "" {
		return fmt.Errorf("config: workers.common.agent_addr is required")
	}
	if c.Workers.Pipeline.Concurrency < 1 {
		return fmt.Errorf("config: workers.pipeline.concurrency must be at least 1")
	}
	switch c.Workers.Pipeline.Runtime {
	case "baremetal", "docker":
	default:
		return fmt.Errorf("config: workers.pipeline.runtime must be baremetal or docker")
	}
	return nil
}

func ValidateNotebook(c RootConfig) error {
	if c.Workers.Common.AgentAddr == "" {
		return fmt.Errorf("config: workers.common.agent_addr is required")
	}
	switch c.Workers.Notebook.Mode {
	case "process", "docker":
	default:
		return fmt.Errorf("config: workers.notebook.mode must be process or docker")
	}
	p := strings.Split(c.Workers.Notebook.PortRange, "-")
	if len(p) != 2 {
		return fmt.Errorf("config: workers.notebook.port_range must be START-END")
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil || a < 1 || b > 65535 || a > b {
		return fmt.Errorf("config: workers.notebook.port_range is invalid")
	}
	if err := unique("workers.notebook.gpus", c.Workers.Notebook.GPUs); err != nil {
		return err
	}
	if err := validateNotebookDocker(c.Workers.Notebook.Docker); err != nil {
		return err
	}
	return nil
}

func ValidateServing(c RootConfig) error {
	if c.Workers.Common.AgentAddr == "" {
		return fmt.Errorf("config: workers.common.agent_addr is required")
	}
	switch c.Workers.Serving.Mode {
	case "process", "docker":
	default:
		return fmt.Errorf("config: workers.serving.mode must be process or docker")
	}
	return unique("workers.serving.gpus", c.Workers.Serving.GPUs)
}

func ValidateK8s(c RootConfig) error {
	if c.Workers.Common.MasterURL == "" {
		return fmt.Errorf("config: workers.common.master_url is required")
	}
	if c.Workers.Common.AgentAddr == "" {
		return fmt.Errorf("config: workers.common.agent_addr is required")
	}
	if c.Workers.K8s.Cluster == "" {
		return fmt.Errorf("config: workers.k8s.cluster is required")
	}
	if c.Workers.K8s.InCluster && c.Workers.K8s.Kubeconfig != "" {
		return fmt.Errorf("config: workers.k8s.in_cluster and kubeconfig are mutually exclusive")
	}
	if !c.Workers.K8s.InCluster && c.Workers.K8s.Kubeconfig == "" {
		return fmt.Errorf("config: workers.k8s.kubeconfig is required outside the cluster")
	}
	for _, d := range c.Workers.K8s.Enabled {
		if d != "pipeline" && d != "notebook" && d != "serving" {
			return fmt.Errorf("config: workers.k8s.enabled contains invalid domain %q", d)
		}
	}
	if size := c.Workers.K8s.Notebook.StorageSize; size != "" {
		qty, err := resource.ParseQuantity(size)
		if err != nil || qty.Sign() <= 0 {
			return fmt.Errorf("config: workers.k8s.notebook.storage_size is invalid")
		}
	}
	switch c.Workers.K8s.Pipeline.AgentImagePullPolicy {
	case "", "Always", "IfNotPresent", "Never":
	default:
		return fmt.Errorf("config: workers.k8s.pipeline.agent_image_pull_policy must be Always, IfNotPresent, or Never")
	}
	allowed := make(map[string]bool, len(c.Workers.K8s.Namespaces))
	for _, ns := range c.Workers.K8s.Namespaces {
		allowed[ns] = true
	}
	if len(allowed) > 0 {
		for _, item := range []struct{ key, namespace string }{{"workers.k8s.pipeline.namespace", c.Workers.K8s.Pipeline.Namespace}, {"workers.k8s.notebook.namespace", c.Workers.K8s.Notebook.Namespace}, {"workers.k8s.serving.namespace", c.Workers.K8s.Serving.Namespace}} {
			if item.namespace != "" && !allowed[item.namespace] {
				return fmt.Errorf("config: %s must be included in workers.k8s.namespaces", item.key)
			}
		}
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

func validateNotebookDocker(c NotebookDockerConfig) error {
	if c.CPUs != "" {
		cpus, err := strconv.ParseFloat(c.CPUs, 64)
		if err != nil || cpus <= 0 {
			return fmt.Errorf("config: workers.notebook.docker.cpus is invalid")
		}
	}
	for _, item := range []struct{ key, value string }{{"memory", c.Memory}, {"shm_size", c.ShmSize}} {
		if item.value == "" {
			continue
		}
		n, err := units.RAMInBytes(item.value)
		if err != nil || n <= 0 {
			return fmt.Errorf("config: workers.notebook.docker.%s is invalid", item.key)
		}
	}
	names := map[string]bool{}
	for _, v := range c.Volumes {
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("config: workers.notebook.docker.volumes name is required")
		}
		if names[v.Name] {
			return fmt.Errorf("config: workers.notebook.docker.volumes contains duplicate name %q", v.Name)
		}
		names[v.Name] = true
		if !filepath.IsAbs(v.HostPath) {
			return fmt.Errorf("config: workers.notebook.docker.volumes[%s].host_path must be absolute", v.Name)
		}
		if !filepath.IsAbs(v.ContainerPath) {
			return fmt.Errorf("config: workers.notebook.docker.volumes[%s].container_path must be absolute", v.Name)
		}
	}
	return nil
}
