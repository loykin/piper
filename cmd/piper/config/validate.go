package config

import (
	"fmt"
	"strings"
)

const (
	InfrastructureBaremetal = "baremetal"
	InfrastructureDocker    = "docker"
	InfrastructureK8s       = "k8s"
)

const (
	DeploymentModeHome   = "home"
	DeploymentModeMember = "member"
)

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
	if err := validateRuntime(c); err != nil {
		return err
	}
	if err := validateDeployment(c); err != nil {
		return err
	}
	return nil
}

// validateDeployment validates deployment.mode (fed.md §13.5). runtime.type
// is validated unconditionally by validateRuntime (called before this), so
// member mode's runtime requirement doesn't need to be re-checked here.
func validateDeployment(c RootConfig) error {
	switch c.Deployment.Mode {
	case "", DeploymentModeHome:
		return nil
	case DeploymentModeMember:
		if c.Home.ID == "" {
			return fmt.Errorf("config: home.id is required when deployment.mode is member")
		}
		if c.Home.URL == "" {
			return fmt.Errorf("config: home.url is required when deployment.mode is member")
		}
		if c.Home.EnrollmentToken == "" {
			return fmt.Errorf("config: home.enrollment_token is required when deployment.mode is member")
		}
		return nil
	default:
		return fmt.Errorf("config: deployment.mode must be home, member, or empty")
	}
}

// validateRuntime validates runtime.type — required; there is no
// remote-worker fallback for an empty value.
func validateRuntime(c RootConfig) error {
	switch c.Runtime.Type {
	case "":
		return fmt.Errorf("config: runtime.type is required (k8s, docker, or baremetal)")
	case InfrastructureK8s:
		return validateK8sDirectRuntime(c)
	case InfrastructureDocker:
		return validateDockerDirectRuntime(c)
	case InfrastructureBaremetal:
		return validateBaremetalDirectRuntime(c)
	default:
		return fmt.Errorf("config: runtime.type must be k8s, docker, baremetal, or empty")
	}
}

func validateK8sDirectRuntime(c RootConfig) error {
	if c.Runtime.InCluster && c.Runtime.Kubeconfig != "" {
		return fmt.Errorf("config: runtime.in_cluster and runtime.kubeconfig are mutually exclusive")
	}
	if !c.Runtime.InCluster && c.Runtime.Kubeconfig == "" {
		return fmt.Errorf("config: runtime.kubeconfig is required outside the cluster")
	}
	if len(c.Runtime.Namespaces) == 0 {
		return fmt.Errorf("config: runtime.namespaces must contain at least one allowed namespace")
	}
	if err := unique("runtime.namespaces", c.Runtime.Namespaces); err != nil {
		return err
	}
	switch c.Runtime.PipelineRunner.ImagePullPolicy {
	case "", "Always", "IfNotPresent", "Never":
	default:
		return fmt.Errorf("config: runtime.pipeline_runner.image_pull_policy must be Always, IfNotPresent, or Never")
	}
	if !c.Storage.Disabled && (c.Storage.URL == "" || strings.HasPrefix(c.Storage.URL, "file://")) && strings.TrimSpace(c.Runtime.WorkloadURL) == "" {
		return fmt.Errorf("config: runtime.workload_url is required when using the built-in file artifact store")
	}
	return nil
}

// validateDockerDirectRuntime validates runtime.type: docker (direct,
// in-process Docker pipeline execution). Docker needs workload_url for the
// same reason k8s does — a container cannot reach the host's local
// filesystem directly.
func validateDockerDirectRuntime(c RootConfig) error {
	var docker RuntimeDockerConfig
	if c.Runtime.Docker != nil {
		docker = *c.Runtime.Docker
	}
	if docker.Concurrency < 0 {
		return fmt.Errorf("config: runtime.docker.concurrency must not be negative")
	}
	if !c.Storage.Disabled && (c.Storage.URL == "" || strings.HasPrefix(c.Storage.URL, "file://")) && strings.TrimSpace(docker.WorkloadURL) == "" {
		return fmt.Errorf("config: runtime.docker.workload_url is required when using the built-in file artifact store")
	}
	return nil
}

// validateBaremetalDirectRuntime validates runtime.type: baremetal (direct,
// in-process subprocess pipeline execution). Unlike k8s/docker, baremetal
// shares the host filesystem directly, so no workload_url is required.
func validateBaremetalDirectRuntime(c RootConfig) error {
	var baremetal RuntimeBaremetalConfig
	if c.Runtime.Baremetal != nil {
		baremetal = *c.Runtime.Baremetal
	}
	if baremetal.Concurrency < 0 {
		return fmt.Errorf("config: runtime.baremetal.concurrency must not be negative")
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
