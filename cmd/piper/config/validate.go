package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/loykin/piper/pkg/statsstore"
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
	if c.Stats.Spool.MaxBytes < 0 {
		return fmt.Errorf("config: stats.spool.max_bytes must not be negative")
	}
	if c.Stats.Logs.Retention < 0 {
		return fmt.Errorf("config: stats.logs.retention must not be negative")
	}
	if c.Stats.Metrics.Retention < 0 {
		return fmt.Errorf("config: stats.metrics.retention must not be negative")
	}
	if err := statsstore.ValidateBackendURL("logs", c.Stats.Logs.URL); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := statsstore.ValidateBackendURL("metrics", c.Stats.Metrics.URL); err != nil {
		return fmt.Errorf("config: %w", err)
	}
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
	if err := validateNotebookExecution(c.NotebookExecution); err != nil {
		return err
	}
	if err := validateIntegrations(c.Integrations); err != nil {
		return err
	}
	if c.MCP.Enabled && len(c.MCP.AllowedOrigins) == 0 {
		return fmt.Errorf("config: mcp.allowed_origins must not be empty when mcp.enabled is true")
	}
	if c.MCP.SessionTTL < 0 {
		return fmt.Errorf("config: mcp.session_ttl must not be negative")
	}
	return nil
}

func validateNotebookExecution(c NotebookExecutionConfig) error {
	switch c.MCPPolicy {
	case "", "disabled", "approval_required", "allowed":
	default:
		return fmt.Errorf("config: notebook_execution.mcp_policy must be disabled, approval_required, or allowed")
	}
	if c.MaxRunningPerNotebook < 0 || c.MaxKernelsPerNotebook < 0 || c.MaxQueuedPerProject < 0 {
		return fmt.Errorf("config: notebook_execution concurrency limits must not be negative")
	}
	if c.KernelIdleTTL < 0 || c.CellTimeout < 0 || c.ExecutionTimeout < 0 {
		return fmt.Errorf("config: notebook_execution durations must not be negative")
	}
	if c.InlineOutputBytes < 0 || c.FileReadBytes < 0 {
		return fmt.Errorf("config: notebook_execution byte limits must not be negative")
	}
	return nil
}

func validateIntegrations(c IntegrationsConfig) error {
	m := c.MLflow
	if m.DispatcherConcurrency < 0 || m.BatchSize < 0 || m.MaxAttemptsBeforeDead < 0 {
		return fmt.Errorf("config: integrations.mlflow numeric limits must not be negative")
	}
	if m.RequestTimeout < 0 || m.LeaseDuration < 0 || m.PollInterval < 0 {
		return fmt.Errorf("config: integrations.mlflow durations must not be negative")
	}
	for _, host := range m.AllowedHosts {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("config: integrations.mlflow.allowed_hosts must not contain empty values")
		}
	}
	for _, cidr := range m.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("config: integrations.mlflow.allowed_cidrs contains invalid CIDR %q", cidr)
		}
	}
	return nil
}

// validateDeployment validates deployment.mode (fed.md §13.5). runtime.type
// is validated unconditionally by validateRuntime (called before this), so
// member mode's runtime requirement doesn't need to be re-checked here.
func validateDeployment(c RootConfig) error {
	switch c.Deployment.Mode {
	case "", DeploymentModeHome:
		return validateHomeFederation(c)
	case DeploymentModeMember:
		if c.Home.ID == "" {
			return fmt.Errorf("config: home.id is required when deployment.mode is member")
		}
		if c.Home.URL == "" {
			return fmt.Errorf("config: home.url is required when deployment.mode is member")
		}
		homeURL, err := url.Parse(c.Home.URL)
		if err != nil || homeURL.Host == "" || (homeURL.Scheme != "http" && homeURL.Scheme != "https") {
			return fmt.Errorf("config: home.url must be an http or https URL when deployment.mode is member")
		}
		if c.Home.EnrollmentToken == "" {
			return fmt.Errorf("config: home.enrollment_token is required when deployment.mode is member")
		}
		return nil
	default:
		return fmt.Errorf("config: deployment.mode must be home, member, or empty")
	}
}

func validateHomeFederation(c RootConfig) error {
	if c.Home.TunnelAddr == "" {
		if len(c.Home.Members) != 0 || len(c.Home.Projects) != 0 {
			return fmt.Errorf("config: home.tunnel_addr is required when home.members or home.projects is configured")
		}
		return nil
	}
	if c.Home.ID == "" {
		return fmt.Errorf("config: home.id is required when home.tunnel_addr is configured")
	}
	if len(c.Home.Members) == 0 {
		return fmt.Errorf("config: home.members must contain at least one member when home.tunnel_addr is configured")
	}
	for memberID, token := range c.Home.Members {
		if strings.TrimSpace(memberID) == "" || strings.TrimSpace(token) == "" {
			return fmt.Errorf("config: home.members must contain non-empty member IDs and tokens")
		}
	}
	for projectID, memberID := range c.Home.Projects {
		if strings.TrimSpace(projectID) == "" || strings.TrimSpace(memberID) == "" {
			return fmt.Errorf("config: home.projects must contain non-empty project and member IDs")
		}
		if _, ok := c.Home.Members[memberID]; !ok {
			return fmt.Errorf("config: home.projects[%q] references unknown member %q", projectID, memberID)
		}
	}
	return nil
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
		return fmt.Errorf("config: runtime.type must be k8s, docker, or baremetal")
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
