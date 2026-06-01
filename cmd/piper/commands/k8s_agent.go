package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/piper/piper/pkg/k8sagent"
)

func newK8sAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k8s-agent",
		Short: "Start a cluster-local K8s agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			masterURL, _ := cmd.Flags().GetString("master")
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
			pipelineAgentImage, _ := cmd.Flags().GetString("pipeline-agent-image")
			storageClass, _ := cmd.Flags().GetString("storage-class")
			storageSize, _ := cmd.Flags().GetString("storage-size")
			defaultImage, _ := cmd.Flags().GetString("default-image")
			agentImagePullPolicy, _ := cmd.Flags().GetString("agent-image-pull-policy")
			s3Endpoint, _ := cmd.Flags().GetString("s3-endpoint")
			s3AccessKey, _ := cmd.Flags().GetString("s3-access-key")
			s3SecretKey, _ := cmd.Flags().GetString("s3-secret-key")
			s3Bucket, _ := cmd.Flags().GetString("s3-bucket")
			s3UseSSL, _ := cmd.Flags().GetBool("s3-use-ssl")
			if id == "" {
				id = uuid.NewString()
			}
			var namespaces []string
			for _, ns := range strings.Split(namespacesStr, ",") {
				if trimmed := strings.TrimSpace(ns); trimmed != "" {
					namespaces = append(namespaces, trimmed)
				}
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			k8sClient, err := buildK8sAgentClient(kubeconfig, inCluster)
			if err != nil {
				return err
			}

			return k8sagent.New(k8sagent.Config{
				MasterURL:            masterURL,
				Token:                token,
				ID:                   id,
				ClusterName:          cluster,
				Namespaces:           namespaces,
				K8sClient:            k8sClient,
				NotebookNamespace:    notebookNamespace,
				ServingNamespace:     servingNamespace,
				PipelineNamespace:    pipelineNamespace,
				NotebookImage:        notebookImage,
				PipelineAgentImage:   pipelineAgentImage,
				StorageClass:         storageClass,
				StorageSize:          storageSize,
				DefaultImage:         defaultImage,
				AgentImagePullPolicy: agentImagePullPolicy,
				S3Endpoint:           s3Endpoint,
				S3AccessKey:          s3AccessKey,
				S3SecretKey:          s3SecretKey,
				S3Bucket:             s3Bucket,
				S3UseSSL:             s3UseSSL,
			}).Run(ctx)
		},
	}

	cmd.Flags().String("master", "", "master server URL (required)")
	cmd.Flags().String("token", "", "bearer token for master API")
	cmd.Flags().String("id", "", "agent ID (default: random UUID)")
	cmd.Flags().String("cluster", "", "cluster name reported to master (required)")
	cmd.Flags().String("namespaces", "", "comma-separated namespaces this agent may manage")
	cmd.Flags().String("kubeconfig", "", "kubeconfig path for out-of-cluster execution")
	cmd.Flags().Bool("in-cluster", true, "use in-cluster Kubernetes credentials")
	cmd.Flags().String("notebook-namespace", "", "namespace for notebook StatefulSets/PVCs (default: first namespace or default)")
	cmd.Flags().String("serving-namespace", "", "namespace for serving Deployments/Services (default: first namespace or default)")
	cmd.Flags().String("pipeline-namespace", "", "namespace for pipeline Jobs (default: first namespace or default)")
	cmd.Flags().String("notebook-image", "", "default notebook image")
	cmd.Flags().String("pipeline-agent-image", "piper/piper:latest", "image containing the piper CLI for pipeline Job init containers")
	cmd.Flags().String("agent-image-pull-policy", "", "image pull policy for pipeline Job init containers")
	cmd.Flags().String("default-image", "", "fallback container image for pipeline steps")
	cmd.Flags().String("storage-class", "", "storage class for notebook PVCs")
	cmd.Flags().String("storage-size", "", "default notebook PVC size (default 10Gi)")
	cmd.Flags().String("s3-endpoint", "", "S3 endpoint for pipeline artifact transfer")
	cmd.Flags().String("s3-access-key", "", "S3 access key for pipeline artifact transfer")
	cmd.Flags().String("s3-secret-key", "", "S3 secret key for pipeline artifact transfer")
	cmd.Flags().String("s3-bucket", "", "S3 bucket for pipeline artifact transfer")
	cmd.Flags().Bool("s3-use-ssl", false, "use SSL for S3 artifact transfer")
	_ = cmd.MarkFlagRequired("master")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func buildK8sAgentClient(kubeconfig string, inCluster bool) (kubernetes.Interface, error) {
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
		return nil, fmt.Errorf("k8s agent config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s agent client: %w", err)
	}
	return client, nil
}
