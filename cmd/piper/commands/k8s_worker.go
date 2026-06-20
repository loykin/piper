package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	k8sworker "github.com/piper/piper/internal/k8sworker"
)

func newK8sWorkerCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k8s-worker",
		Short: "Start a cluster-local K8s worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateK8s(root); err != nil {
				return err
			}
			c, common := root.Workers.K8s, root.Workers.Common
			cluster := c.Cluster
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
			storageToken := common.StorageToken
			if storageToken == "" {
				storageToken = root.Storage.Token
			}
			return k8sworker.New(k8sworker.Config{
				Agent: k8sworker.AgentConfig{
					MasterURL:   common.MasterURL,
					WorkerToken: common.WorkerToken,
					ID:          id,
					ClusterName: cluster,
					Labels:      common.Labels,
				},
				StorageToken: storageToken,
				K8s: k8sworker.K8sConfig{
					Client:                      k8sClient,
					Namespaces:                  c.Namespaces,
					EnabledDomains:              c.Enabled,
					NotebookInfrastructureImage: c.Notebook.InfrastructureImage,
					PipelineWorkerImage:         c.Pipeline.RunnerImage,
					AgentImagePullPolicy:        c.Pipeline.RunnerImagePullPolicy,
				},
				StorageURL:      root.Storage.URL,
				ResultOutboxDir: c.ResultOutboxDir,
			}).Run(ctx)
		},
	}

	cmd.Flags().String("master-url", "", "master server URL (required)")
	cmd.Flags().String("worker-token", "", "worker tunnel authentication token")
	cmd.Flags().String("state-dir", "", "directory for persistent worker identity and state")
	cmd.Flags().String("cluster", "", "cluster name reported to master (required)")
	cmd.Flags().StringSlice("namespaces", nil, "namespaces this worker may manage")
	cmd.Flags().StringSlice("enable", nil, "domains to enable: pipeline, notebook, serving")
	cmd.Flags().String("kubeconfig", "", "kubeconfig path for out-of-cluster execution")
	cmd.Flags().Bool("in-cluster", false, "use in-cluster Kubernetes credentials (effective default: true)")
	cmd.Flags().String("notebook-infrastructure-image", "", "image containing piper for notebook volume-browser pods")
	cmd.Flags().String("runner-image", "", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("runner-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("result-outbox-dir", "", "durable directory for unacknowledged pipeline results (default: system temp directory)")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("workers.common.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))
	loader.MustBindFlag("workers.common.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("workers.k8s.cluster", cmd.Flags().Lookup("cluster"))
	loader.MustBindFlag("workers.k8s.namespaces", cmd.Flags().Lookup("namespaces"))
	loader.MustBindFlag("workers.k8s.enabled", cmd.Flags().Lookup("enable"))
	loader.MustBindFlag("workers.k8s.kubeconfig", cmd.Flags().Lookup("kubeconfig"))
	loader.MustBindFlag("workers.k8s.in_cluster", cmd.Flags().Lookup("in-cluster"))
	loader.MustBindFlag("workers.k8s.notebook.infrastructure_image", cmd.Flags().Lookup("notebook-infrastructure-image"))
	loader.MustBindFlag("workers.k8s.pipeline.runner_image", cmd.Flags().Lookup("runner-image"))
	loader.MustBindFlag("workers.k8s.pipeline.runner_image_pull_policy", cmd.Flags().Lookup("runner-image-pull-policy"))
	loader.MustBindFlag("workers.k8s.result_outbox_dir", cmd.Flags().Lookup("result-outbox-dir"))
	return cmd
}

func buildK8sWorkerClient(kubeconfig string, inCluster bool) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if inCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		if kubeconfig == "" {
			return nil, fmt.Errorf("--kubeconfig is required when --in-cluster=false")
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
