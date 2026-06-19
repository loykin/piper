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
			id, cluster := c.ID, c.Cluster
			if id == "" {
				id = stableWorkerID("k8s", cluster)
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			k8sClient, err := buildK8sWorkerClient(c.Kubeconfig, c.InCluster)
			if err != nil {
				return err
			}
			podDefaults, err := parsePodDefaults(c.Notebook.PodDefaults)
			if err != nil {
				return fmt.Errorf("config: workers.k8s.notebook.pod_defaults: %w", err)
			}

			storageToken := common.StorageToken
			if storageToken == "" {
				storageToken = root.Storage.Token
			}
			return k8sworker.New(k8sworker.Config{
				Agent: k8sworker.AgentConfig{
					Addr:        common.AgentAddr,
					WorkerToken: common.WorkerToken,
					ID:          id,
					ClusterName: cluster,
				},
				Master: k8sworker.MasterConfig{
					URL:          common.MasterURL,
					WorkerToken:  common.WorkerToken,
					StorageToken: storageToken,
				},
				K8s: k8sworker.K8sConfig{
					Client:               k8sClient,
					Namespaces:           c.Namespaces,
					EnabledDomains:       c.Enabled,
					NotebookNamespace:    c.Notebook.Namespace,
					ServingNamespace:     c.Serving.Namespace,
					PipelineNamespace:    c.Pipeline.Namespace,
					NotebookImage:        c.Notebook.Image,
					PipelineWorkerImage:  c.Pipeline.WorkerImage,
					StorageClass:         c.Notebook.StorageClass,
					StorageSize:          c.Notebook.StorageSize,
					DefaultImage:         c.Pipeline.DefaultImage,
					AgentImagePullPolicy: c.Pipeline.AgentImagePullPolicy,
					PodDefaults:          podDefaults,
				},
				StorageURL:      root.Storage.URL,
				ResultOutboxDir: c.ResultOutboxDir,
			}).Run(ctx)
		},
	}

	cmd.Flags().String("master-url", "", "master server URL (required)")
	cmd.Flags().String("agent-addr", "", "piper master gRPC agent address, e.g. piper-server:9090 (required)")
	cmd.Flags().String("worker-token", "", "worker token for master callbacks")
	cmd.Flags().String("id", "", "worker ID (default: stable k8s-<cluster>)")
	cmd.Flags().String("cluster", "", "cluster name reported to master (required)")
	cmd.Flags().StringSlice("namespaces", nil, "namespaces this worker may manage")
	cmd.Flags().StringSlice("enable", nil, "domains to enable: pipeline, notebook, serving")
	cmd.Flags().String("kubeconfig", "", "kubeconfig path for out-of-cluster execution")
	cmd.Flags().Bool("in-cluster", false, "use in-cluster Kubernetes credentials (effective default: true)")
	cmd.Flags().String("notebook-namespace", "", "namespace for notebook StatefulSets/PVCs (default: first namespace or default)")
	cmd.Flags().String("serving-namespace", "", "namespace for serving Deployments/Services (default: first namespace or default)")
	cmd.Flags().String("pipeline-namespace", "", "namespace for pipeline Jobs (default: first namespace or default)")
	cmd.Flags().String("notebook-image", "", "default notebook image")
	cmd.Flags().String("pipeline-worker-image", "", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("agent-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("default-image", "", "fallback container image for pipeline steps")
	cmd.Flags().String("storage-class", "", "storage class for notebook PVCs")
	cmd.Flags().String("storage-size", "", "default notebook PVC size (default 10Gi)")
	cmd.Flags().String("result-outbox-dir", "", "durable directory for unacknowledged pipeline results (default: system temp directory)")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("workers.common.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("workers.common.agent_addr", cmd.Flags().Lookup("agent-addr"))
	loader.MustBindFlag("workers.common.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("workers.common.storage_token", cmd.Flags().Lookup("storage-token"))
	loader.MustBindFlag("workers.k8s.id", cmd.Flags().Lookup("id"))
	loader.MustBindFlag("workers.k8s.cluster", cmd.Flags().Lookup("cluster"))
	loader.MustBindFlag("workers.k8s.namespaces", cmd.Flags().Lookup("namespaces"))
	loader.MustBindFlag("workers.k8s.enabled", cmd.Flags().Lookup("enable"))
	loader.MustBindFlag("workers.k8s.kubeconfig", cmd.Flags().Lookup("kubeconfig"))
	loader.MustBindFlag("workers.k8s.in_cluster", cmd.Flags().Lookup("in-cluster"))
	loader.MustBindFlag("workers.k8s.notebook.namespace", cmd.Flags().Lookup("notebook-namespace"))
	loader.MustBindFlag("workers.k8s.serving.namespace", cmd.Flags().Lookup("serving-namespace"))
	loader.MustBindFlag("workers.k8s.pipeline.namespace", cmd.Flags().Lookup("pipeline-namespace"))
	loader.MustBindFlag("workers.k8s.notebook.image", cmd.Flags().Lookup("notebook-image"))
	loader.MustBindFlag("workers.k8s.pipeline.worker_image", cmd.Flags().Lookup("pipeline-worker-image"))
	loader.MustBindFlag("workers.k8s.notebook.storage_class", cmd.Flags().Lookup("storage-class"))
	loader.MustBindFlag("workers.k8s.notebook.storage_size", cmd.Flags().Lookup("storage-size"))
	loader.MustBindFlag("workers.k8s.pipeline.default_image", cmd.Flags().Lookup("default-image"))
	loader.MustBindFlag("workers.k8s.pipeline.agent_image_pull_policy", cmd.Flags().Lookup("agent-image-pull-policy"))
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
