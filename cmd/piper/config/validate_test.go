package config

import (
	"strings"
	"testing"
)

func TestValidateServerAllowsRuntimeGeneratedSecrets(t *testing.T) {
	baremetal := RuntimeConfig{Type: InfrastructureBaremetal}
	if err := ValidateServer(RootConfig{Runtime: baremetal}); err != nil {
		t.Fatalf("runtime-generated secrets rejected: %v", err)
	}
	if err := ValidateServer(RootConfig{Runtime: baremetal, Server: ServerConfig{AllowInsecureTrustedMode: true}}); err != nil {
		t.Fatalf("explicit trusted mode rejected: %v", err)
	}
	if err := ValidateServer(RootConfig{Runtime: baremetal, Server: ServerConfig{AuthSigningKey: "test-signing-key"}}); err != nil {
		t.Fatalf("signing key rejected: %v", err)
	}
}

func TestValidateServerAcceptsK8sRuntime(t *testing.T) {
	cfg := RootConfig{
		Storage: StorageConfig{Disabled: true},
		Runtime: RuntimeConfig{Type: InfrastructureK8s, InCluster: true, Namespaces: []string{"piper"}},
	}
	if err := ValidateServer(cfg); err != nil {
		t.Fatalf("K8s runtime rejected: %v", err)
	}
}

func TestValidateDeploymentMemberRequiresHomeFields(t *testing.T) {
	base := RootConfig{
		Storage:    StorageConfig{Disabled: true},
		Deployment: DeploymentConfig{Mode: DeploymentModeMember},
		Runtime:    RuntimeConfig{Type: InfrastructureBaremetal},
	}
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "home.id") {
		t.Fatalf("expected home.id error, got: %v", err)
	}
	base.Home.ID = "home-1"
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "home.url") {
		t.Fatalf("expected home.url error, got: %v", err)
	}
	base.Home.URL = "https://home.example.com"
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "home.enrollment_token") {
		t.Fatalf("expected home.enrollment_token error, got: %v", err)
	}
}

func TestValidateDeploymentMemberRequiresRuntimeType(t *testing.T) {
	cfg := RootConfig{
		Storage:    StorageConfig{Disabled: true},
		Deployment: DeploymentConfig{Mode: DeploymentModeMember},
		Home:       HomeConfig{ID: "home-1", URL: "https://home.example.com", EnrollmentToken: "secret"},
	}
	if err := ValidateServer(cfg); err == nil || !strings.Contains(err.Error(), "runtime.type") {
		t.Fatalf("expected runtime.type error, got: %v", err)
	}
}

func TestValidateDeploymentMemberAccepted(t *testing.T) {
	cfg := RootConfig{
		Storage:    StorageConfig{Disabled: true},
		Deployment: DeploymentConfig{Mode: DeploymentModeMember, MemberID: "member-1"},
		Home:       HomeConfig{ID: "home-1", URL: "https://home.example.com", EnrollmentToken: "secret"},
		Runtime:    RuntimeConfig{Type: InfrastructureBaremetal},
	}
	if err := ValidateServer(cfg); err != nil {
		t.Fatalf("valid member config rejected: %v", err)
	}
}

func TestValidateDeploymentMemberRejectsInvalidHomeURLScheme(t *testing.T) {
	cfg := RootConfig{
		Storage:    StorageConfig{Disabled: true},
		Deployment: DeploymentConfig{Mode: DeploymentModeMember, MemberID: "member-1"},
		Home:       HomeConfig{ID: "home-1", URL: "grpc://home.example.com", EnrollmentToken: "secret"},
		Runtime:    RuntimeConfig{Type: InfrastructureBaremetal},
	}
	if err := ValidateServer(cfg); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("expected home.url scheme error, got: %v", err)
	}
}

func TestValidateDeploymentRejectsUnknownMode(t *testing.T) {
	cfg := RootConfig{
		Runtime:    RuntimeConfig{Type: InfrastructureBaremetal},
		Deployment: DeploymentConfig{Mode: "bogus"},
	}
	if err := ValidateServer(cfg); err == nil || !strings.Contains(err.Error(), "deployment.mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDeploymentHomeFederation(t *testing.T) {
	base := RootConfig{
		Storage:    StorageConfig{Disabled: true},
		Runtime:    RuntimeConfig{Type: InfrastructureBaremetal},
		Deployment: DeploymentConfig{Mode: DeploymentModeHome},
	}
	base.Home.Members = map[string]string{"member-1": "token-1"}
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "home.tunnel_addr") {
		t.Fatalf("expected tunnel_addr error, got: %v", err)
	}
	base.Home.TunnelAddr = ":9090"
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "home.id") {
		t.Fatalf("expected home.id error, got: %v", err)
	}
	base.Home.ID = "home-1"
	base.Home.Projects = map[string]string{"project-1": "unknown"}
	if err := ValidateServer(base); err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("expected unknown member error, got: %v", err)
	}
	base.Home.Projects["project-1"] = "member-1"
	if err := ValidateServer(base); err != nil {
		t.Fatalf("valid home federation config rejected: %v", err)
	}
}

func TestValidateDeploymentHomeTunnelRequiresMembers(t *testing.T) {
	cfg := RootConfig{
		Storage: StorageConfig{Disabled: true},
		Runtime: RuntimeConfig{Type: InfrastructureBaremetal},
		Home:    HomeConfig{ID: "home-1", TunnelAddr: ":9090"},
	}
	if err := ValidateServer(cfg); err == nil || !strings.Contains(err.Error(), "home.members") {
		t.Fatalf("expected home.members error, got: %v", err)
	}
}

func TestRuntimeConfigLoadsFromFile(t *testing.T) {
	loader := NewLoader()
	loader.SetConfigFile(writeConfig(t, `version: 4
storage:
  disabled: true
runtime:
  type: k8s
  namespaces: [piper]
  in_cluster: true
  pipeline_runner:
    image: piper:test
    image_pull_policy: Never
`))
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateServer(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.PipelineRunner.Image != "piper:test" {
		t.Fatalf("pipeline runner image = %q", cfg.Runtime.PipelineRunner.Image)
	}
}
