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
			if err := applyK8sCapabilitiesFlag(cmd, &root); err != nil {
				return err
			}
			c, common := root.Worker.K8s, root.Worker
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
					EnabledDomains:              k8sCapabilityNames(c.Capabilities),
					NotebookInfrastructureImage: k8sNotebookInfrastructureImage(c.Capabilities),
					PipelineWorkerImage:         k8sPipelineRunnerImage(c.Capabilities),
					AgentImagePullPolicy:        k8sPipelinePullPolicy(c.Capabilities),
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
	cmd.Flags().StringSlice("capabilities", nil, "capabilities to enable: pipeline, notebook, serving")
	cmd.Flags().String("kubeconfig", "", "kubeconfig path for out-of-cluster execution")
	cmd.Flags().Bool("in-cluster", false, "use in-cluster Kubernetes credentials (effective default: true)")
	cmd.Flags().String("notebook-infrastructure-image", "", "image containing piper for notebook volume-browser pods")
	cmd.Flags().String("runner-image", "", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("runner-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("result-outbox-dir", "", "durable directory for unacknowledged pipeline results (default: system temp directory)")
	cmd.Flags().String("storage-token", "", "artifact storage authentication token")
	loader.MustBindFlag("worker.master_url", cmd.Flags().Lookup("master-url"))
	loader.MustBindFlag("worker.worker_token", cmd.Flags().Lookup("worker-token"))
	loader.MustBindFlag("worker.storage_token", cmd.Flags().Lookup("storage-token"))
	loader.MustBindFlag("worker.state_dir", cmd.Flags().Lookup("state-dir"))
	loader.MustBindFlag("worker.k8s.cluster", cmd.Flags().Lookup("cluster"))
	loader.MustBindFlag("worker.k8s.namespaces", cmd.Flags().Lookup("namespaces"))
	loader.MustBindFlag("worker.k8s.kubeconfig", cmd.Flags().Lookup("kubeconfig"))
	loader.MustBindFlag("worker.k8s.in_cluster", cmd.Flags().Lookup("in-cluster"))
	loader.MustBindFlag("worker.k8s.capabilities.notebook.infrastructure_image", cmd.Flags().Lookup("notebook-infrastructure-image"))
	loader.MustBindFlag("worker.k8s.capabilities.pipeline.runner_image", cmd.Flags().Lookup("runner-image"))
	loader.MustBindFlag("worker.k8s.capabilities.pipeline.runner_image_pull_policy", cmd.Flags().Lookup("runner-image-pull-policy"))
	loader.MustBindFlag("worker.k8s.result_outbox_dir", cmd.Flags().Lookup("result-outbox-dir"))
	return cmd
}

func applyK8sCapabilitiesFlag(cmd *cobra.Command, root *cliconfig.RootConfig) error {
	if root.Worker.K8s == nil && cmd.Flags().Changed("capabilities") {
		root.Worker.K8s = &cliconfig.K8sWorkerConfig{}
	}
	if cmd.Flags().Changed("capabilities") {
		domains, _ := cmd.Flags().GetStringSlice("capabilities")
		previous := root.Worker.K8s.Capabilities
		root.Worker.K8s.Capabilities = cliconfig.K8sCapabilitiesConfig{}
		for _, domain := range domains {
			switch domain {
			case "pipeline":
				root.Worker.K8s.Capabilities.Pipeline = previous.Pipeline
				if root.Worker.K8s.Capabilities.Pipeline == nil {
					root.Worker.K8s.Capabilities.Pipeline = &cliconfig.K8sPipelineConfig{RunnerImage: "piper/piper:latest", RunnerImagePullPolicy: "IfNotPresent"}
				}
			case "notebook":
				root.Worker.K8s.Capabilities.Notebook = previous.Notebook
				if root.Worker.K8s.Capabilities.Notebook == nil {
					root.Worker.K8s.Capabilities.Notebook = &cliconfig.K8sNotebookConfig{}
				}
			case "serving":
				root.Worker.K8s.Capabilities.Serving = previous.Serving
				if root.Worker.K8s.Capabilities.Serving == nil {
					root.Worker.K8s.Capabilities.Serving = &cliconfig.K8sServingConfig{}
				}
			default:
				return fmt.Errorf("--capabilities contains invalid capability %q", domain)
			}
		}
	}
	return cliconfig.ValidateK8s(*root)
}

func k8sCapabilityNames(c cliconfig.K8sCapabilitiesConfig) []string {
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

func k8sNotebookInfrastructureImage(c cliconfig.K8sCapabilitiesConfig) string {
	if c.Notebook == nil {
		return ""
	}
	return c.Notebook.InfrastructureImage
}

func k8sPipelineRunnerImage(c cliconfig.K8sCapabilitiesConfig) string {
	if c.Pipeline == nil {
		return ""
	}
	if c.Pipeline.RunnerImage == "" {
		return "piper/piper:latest"
	}
	return c.Pipeline.RunnerImage
}

func k8sPipelinePullPolicy(c cliconfig.K8sCapabilitiesConfig) string {
	if c.Pipeline == nil {
		return ""
	}
	if c.Pipeline.RunnerImagePullPolicy == "" {
		return "IfNotPresent"
	}
	return c.Pipeline.RunnerImagePullPolicy
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
