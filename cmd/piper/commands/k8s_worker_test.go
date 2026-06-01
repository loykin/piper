package commands

import "testing"

func TestBuildK8sWorkerClientRejectsConflictingConfig(t *testing.T) {
	if _, err := buildK8sWorkerClient("kubeconfig", true); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestBuildK8sWorkerClientRequiresKubeconfigOutOfCluster(t *testing.T) {
	if _, err := buildK8sWorkerClient("", false); err == nil {
		t.Fatal("expected kubeconfig required error")
	}
}
