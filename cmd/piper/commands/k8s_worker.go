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
		Short: "Start a Kubernetes worker",
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			loader.MustBindFlag("worker.master_url", cmd.Flags().Lookup("master-url"))
			loader.MustBindFlag("worker.worker_token", cmd.Flags().Lookup("worker-token"))
			loader.MustBindFlag("worker.state_dir", cmd.Flags().Lookup("state-dir"))
			loader.MustBindFlag("worker.k8s.cluster", cmd.Flags().Lookup("cluster"))
			loader.MustBindFlag("worker.k8s.namespaces", cmd.Flags().Lookup("namespaces"))
			loader.MustBindFlag("worker.k8s.kubeconfig", cmd.Flags().Lookup("kubeconfig"))
			loader.MustBindFlag("worker.k8s.in_cluster", cmd.Flags().Lookup("in-cluster"))
			loader.MustBindFlag("worker.k8s.notebook_volume_browser.image", cmd.Flags().Lookup("notebook-volume-browser-image"))
			loader.MustBindFlag("worker.k8s.pipeline_runner.image", cmd.Flags().Lookup("pipeline-runner-image"))
			loader.MustBindFlag("worker.k8s.pipeline_runner.image_pull_policy", cmd.Flags().Lookup("pipeline-runner-image-pull-policy"))
			loader.MustBindFlag("worker.k8s.result_outbox_dir", cmd.Flags().Lookup("result-outbox-dir"))
			return loadAndLog(loader)
		},
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
			return k8sworker.New(k8sworker.Config{
				Agent: k8sworker.AgentConfig{
					MasterURL:   common.MasterURL,
					WorkerToken: common.WorkerToken,
					ID:          id,
					ClusterName: cluster,
					Labels:      common.Labels,
				},
				K8s: k8sworker.K8sConfig{
					Client:                        k8sClient,
					Namespaces:                    c.Namespaces,
					EnabledDomains:                workerCapabilityNames(root.Worker.Capabilities),
					NotebookVolumeBrowserImage:    k8sNotebookVolumeBrowserImage(*c, root.Worker.Capabilities),
					PipelineRunnerImage:           k8sPipelineRunnerImage(*c, root.Worker.Capabilities),
					PipelineRunnerImagePullPolicy: k8sPipelinePullPolicy(*c, root.Worker.Capabilities),
				},
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
	cmd.Flags().Bool("in-cluster", false, "use in-cluster Kubernetes credentials; otherwise --kubeconfig is required")
	cmd.Flags().String("notebook-volume-browser-image", "", "image containing piper for notebook volume-browser pods")
	cmd.Flags().String("pipeline-runner-image", "", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("pipeline-runner-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("result-outbox-dir", "", "durable directory for unacknowledged pipeline results (default: system temp directory)")
	cmd.Flags().String("storage-url", "", "deprecated; artifact storage is configured on the master")
	cmd.Flags().String("storage-token", "", "deprecated; artifact storage is provided by the master task payload")
	_ = cmd.Flags().MarkDeprecated("storage-url", "artifact storage is configured on the master and delivered with each task")
	_ = cmd.Flags().MarkDeprecated("storage-token", "artifact storage is configured on the master and delivered with each task")
	return cmd
}

func applyK8sCapabilitiesFlag(cmd *cobra.Command, root *cliconfig.RootConfig) error {
	if root.Worker.K8s == nil && cmd.Flags().Changed("capabilities") {
		root.Worker.K8s = &cliconfig.K8sWorkerConfig{}
	}
	if cmd.Flags().Changed("capabilities") {
		domains, _ := cmd.Flags().GetStringSlice("capabilities")
		previous := root.Worker.Capabilities
		root.Worker.Capabilities = cliconfig.WorkerCapabilitiesConfig{}
		for _, domain := range domains {
			switch domain {
			case "pipeline":
				root.Worker.Capabilities.Pipeline = previous.Pipeline
				if root.Worker.Capabilities.Pipeline == nil {
					root.Worker.Capabilities.Pipeline = &cliconfig.PipelineCapabilityConfig{}
				}
			case "notebook":
				root.Worker.Capabilities.Notebook = previous.Notebook
				if root.Worker.Capabilities.Notebook == nil {
					root.Worker.Capabilities.Notebook = &cliconfig.NotebookCapabilityConfig{}
				}
			case "serving":
				root.Worker.Capabilities.Serving = previous.Serving
				if root.Worker.Capabilities.Serving == nil {
					root.Worker.Capabilities.Serving = &cliconfig.ServingCapabilityConfig{}
				}
			default:
				return fmt.Errorf("--capabilities contains invalid capability %q", domain)
			}
		}
	}
	return cliconfig.ValidateK8s(*root)
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
		return "piper/piper:latest"
	}
	return k.NotebookVolumeBrowser.Image
}

func k8sPipelineRunnerImage(k cliconfig.K8sWorkerConfig, c cliconfig.WorkerCapabilitiesConfig) string {
	if c.Pipeline == nil {
		return ""
	}
	if k.PipelineRunner.Image == "" {
		return "piper/piper:latest"
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
