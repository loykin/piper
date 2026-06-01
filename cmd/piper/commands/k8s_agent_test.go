package commands

import "testing"

func TestBuildK8sAgentClientRejectsConflictingConfig(t *testing.T) {
	if _, err := buildK8sAgentClient("kubeconfig", true); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestBuildK8sAgentClientRequiresKubeconfigOutOfCluster(t *testing.T) {
	if _, err := buildK8sAgentClient("", false); err == nil {
		t.Fatal("expected kubeconfig required error")
	}
}
