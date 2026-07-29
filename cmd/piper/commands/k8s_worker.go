package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	k8sworker "github.com/piper/piper/internal/k8sworker"
)

func runK8sWorker(root cliconfig.RootConfig) error {
	if err := cliconfig.ValidateK8s(root); err != nil {
		return err
	}
	c, common := root.Worker.K8s, root.Worker
	id, err := loadOrCreateWorkerID(common.StateDir, "k8s")
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	k8sClient, err := buildK8sWorkerClient(c.Kubeconfig, c.InCluster)
	if err != nil {
		return err
	}
	return k8sworker.New(k8sworker.Config{
		Agent: k8sworker.AgentConfig{
			MasterURL: common.MasterURL, WorkerToken: common.WorkerToken,
			ID: id, ClusterName: c.Cluster, Labels: common.Labels,
		},
		K8s: k8sworker.K8sConfig{
			Client: k8sClient, Namespaces: c.Namespaces,
			EnabledDomains:                workerCapabilityNames(root.Worker.Capabilities),
			NotebookVolumeBrowserImage:    k8sNotebookVolumeBrowserImage(*c, root.Worker.Capabilities),
			PipelineRunnerImage:           k8sPipelineRunnerImage(*c, root.Worker.Capabilities),
			PipelineRunnerImagePullPolicy: k8sPipelinePullPolicy(*c, root.Worker.Capabilities),
		},
		ResultOutboxDir: c.ResultOutboxDir,
	}).Run(ctx)
}

func workerCapabilityNames(c cliconfig.WorkerCapabilitiesConfig) []string {
	var out []string
	if c.Pipeline != nil {
		out = append(out, "pipeline")
	}
	if c.Notebook != nil {
		out = append(out, "notebook")
	}
	if c.Serving != nil {
		out = append(out, "serving")
	}
	return out
}

func k8sNotebookVolumeBrowserImage(k cliconfig.K8sWorkerConfig, c cliconfig.WorkerCapabilitiesConfig) string {
	if c.Notebook == nil {
		return ""
	}
	if k.NotebookVolumeBrowser.Image == "" {
		return "ghcr.io/loykin/piper:latest"
	}
	return k.NotebookVolumeBrowser.Image
}

func k8sPipelineRunnerImage(k cliconfig.K8sWorkerConfig, c cliconfig.WorkerCapabilitiesConfig) string {
	if c.Pipeline == nil {
		return ""
	}
	if k.PipelineRunner.Image == "" {
		return "ghcr.io/loykin/piper:latest"
	}
	return k.PipelineRunner.Image
}

func k8sPipelinePullPolicy(k cliconfig.K8sWorkerConfig, c cliconfig.WorkerCapabilitiesConfig) string {
	if c.Pipeline == nil {
		return ""
	}
	if k.PipelineRunner.ImagePullPolicy == "" {
		return "IfNotPresent"
	}
	return k.PipelineRunner.ImagePullPolicy
}

func buildK8sWorkerClient(kubeconfig string, inCluster bool) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if inCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		if kubeconfig == "" {
			return nil, fmt.Errorf("config: worker.k8s.kubeconfig is required when worker.k8s.in_cluster=false")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s worker config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s worker client: %w", err)
	}
	return client, nil
}
