package commands

import (
	"testing"

	cliconfig "github.com/piper/piper/cmd/piper/config"
)

func TestK8sWorkerInClusterDefaultsFalse(t *testing.T) {
	cmd := newK8sWorkerCmd(cliconfig.NewLoader())
	inCluster, err := cmd.Flags().GetBool("in-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if inCluster {
		t.Fatal("in-cluster must require explicit opt-in")
	}
}

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
