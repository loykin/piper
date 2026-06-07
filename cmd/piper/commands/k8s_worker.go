package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	k8sworker "github.com/piper/piper/pkg/workers/k8s"
)

func newK8sWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k8s-worker",
		Short: "Start a cluster-local K8s worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			masterURL, _ := cmd.Flags().GetString("master")
			agentAddr, _ := cmd.Flags().GetString("agent-addr")
			token, _ := cmd.Flags().GetString("token")
			id, _ := cmd.Flags().GetString("id")
			cluster, _ := cmd.Flags().GetString("cluster")
			namespacesStr, _ := cmd.Flags().GetString("namespaces")
			kubeconfig, _ := cmd.Flags().GetString("kubeconfig")
			inCluster, _ := cmd.Flags().GetBool("in-cluster")
			notebookNamespace, _ := cmd.Flags().GetString("notebook-namespace")
			servingNamespace, _ := cmd.Flags().GetString("serving-namespace")
			pipelineNamespace, _ := cmd.Flags().GetString("pipeline-namespace")
			notebookImage, _ := cmd.Flags().GetString("notebook-image")
			pipelineWorkerImage, _ := cmd.Flags().GetString("pipeline-worker-image")
			storageClass, _ := cmd.Flags().GetString("storage-class")
			storageSize, _ := cmd.Flags().GetString("storage-size")
			defaultImage, _ := cmd.Flags().GetString("default-image")
			agentImagePullPolicy, _ := cmd.Flags().GetString("agent-image-pull-policy")
			storageURL, _ := cmd.Flags().GetString("storage-url")
			resultOutboxDir, _ := cmd.Flags().GetString("result-outbox-dir")
			if id == "" {
				id = stableWorkerID("k8s", cluster)
			}
			var namespaces []string
			for _, ns := range strings.Split(namespacesStr, ",") {
				if trimmed := strings.TrimSpace(ns); trimmed != "" {
					namespaces = append(namespaces, trimmed)
				}
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			k8sClient, err := buildK8sWorkerClient(kubeconfig, inCluster)
			if err != nil {
				return err
			}

			cfg, _ := buildConfig()
			return k8sworker.New(k8sworker.Config{
				Agent: k8sworker.AgentConfig{
					Addr:        agentAddr,
					ID:          id,
					ClusterName: cluster,
				},
				Master: k8sworker.MasterConfig{
					URL:   masterURL,
					Token: token,
				},
				K8s: k8sworker.K8sConfig{
					Client:               k8sClient,
					Namespaces:           namespaces,
					NotebookNamespace:    notebookNamespace,
					ServingNamespace:     servingNamespace,
					PipelineNamespace:    pipelineNamespace,
					NotebookImage:        notebookImage,
					PipelineWorkerImage:  pipelineWorkerImage,
					StorageClass:         storageClass,
					StorageSize:          storageSize,
					DefaultImage:         defaultImage,
					AgentImagePullPolicy: agentImagePullPolicy,
					PodDefaults:          cfg.NotebookK8s.PodDefaults,
				},
				StorageURL:      storageURL,
				ResultOutboxDir: resultOutboxDir,
			}).Run(ctx)
		},
	}

	cmd.Flags().String("master", "", "master server URL (required)")
	cmd.Flags().String("agent-addr", "", "piper master gRPC agent address, e.g. piper-server:9090 (required)")
	cmd.Flags().String("token", "", "bearer token for master API")
	cmd.Flags().String("id", "", "worker ID (default: stable k8s-<cluster>)")
	cmd.Flags().String("cluster", "", "cluster name reported to master (required)")
	cmd.Flags().String("namespaces", "", "comma-separated namespaces this worker may manage")
	cmd.Flags().String("kubeconfig", "", "kubeconfig path for out-of-cluster execution")
	cmd.Flags().Bool("in-cluster", true, "use in-cluster Kubernetes credentials")
	cmd.Flags().String("notebook-namespace", "", "namespace for notebook StatefulSets/PVCs (default: first namespace or default)")
	cmd.Flags().String("serving-namespace", "", "namespace for serving Deployments/Services (default: first namespace or default)")
	cmd.Flags().String("pipeline-namespace", "", "namespace for pipeline Jobs (default: first namespace or default)")
	cmd.Flags().String("notebook-image", "", "default notebook image")
	cmd.Flags().String("pipeline-worker-image", "piper/piper:latest", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("agent-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("default-image", "", "fallback container image for pipeline steps")
	cmd.Flags().String("storage-class", "", "storage class for notebook PVCs")
	cmd.Flags().String("storage-size", "", "default notebook PVC size (default 10Gi)")
	cmd.Flags().String("storage-url", "", "artifact store URL (s3://, file://, http://) for pipeline artifact transfer")
	cmd.Flags().String("result-outbox-dir", "", "durable directory for unacknowledged pipeline results (default: system temp directory)")
	_ = cmd.MarkFlagRequired("master")
	_ = cmd.MarkFlagRequired("agent-addr")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func buildK8sWorkerClient(kubeconfig string, inCluster bool) (kubernetes.Interface, error) {
	if inCluster && kubeconfig != "" {
		return nil, fmt.Errorf("--in-cluster and --kubeconfig are mutually exclusive")
	}
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
