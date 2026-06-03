package k8sworker

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	k8snotebook "github.com/piper/piper/pkg/workers/k8s/notebook"
	k8spipeline "github.com/piper/piper/pkg/workers/k8s/pipeline"
	k8sserving "github.com/piper/piper/pkg/workers/k8s/serving"
)

type Config struct {
	// AgentAddr is the gRPC address of the piper master agent server, e.g. "master:9090".
	AgentAddr string
	// MasterURL is the HTTP address of the piper master (used by pipeline job pods for callbacks).
	MasterURL   string
	Token       string
	ID          string
	ClusterName string
	Namespaces  []string
	K8sClient   kubernetes.Interface

	NotebookNamespace    string
	ServingNamespace     string
	PipelineNamespace    string
	NotebookImage        string
	PipelineWorkerImage  string
	StorageClass         string
	StorageSize          string
	AgentImagePullPolicy string
	DefaultImage         string
	TTLAfterFinished     *int32
	StorageURL           string
	PodDefaults          corev1.PodTemplateSpec
}

type Worker struct {
	cfg    Config
	client *grpcagent.Client
}

func New(cfg Config) *Worker {
	capabilities := []string{iagent.CapabilityK8s}
	if cfg.K8sClient != nil {
		capabilities = append(capabilities, iagent.CapabilityNotebook, iagent.CapabilityServing, iagent.CapabilityPipeline)
	}

	client := grpcagent.NewClient(grpcagent.ClientConfig{
		AgentAddr:    cfg.AgentAddr,
		AgentID:      cfg.ID,
		Kind:         iagent.KindK8s,
		ClusterName:  cfg.ClusterName,
		Capabilities: capabilities,
	})

	if cfg.K8sClient != nil {
		k8snotebook.Register(client.Dispatcher(), k8snotebook.Config{
			AgentID:      cfg.ID,
			ClusterName:  cfg.ClusterName,
			Namespaces:   cfg.Namespaces,
			Client:       cfg.K8sClient,
			Namespace:    cfg.NotebookNamespace,
			Image:        cfg.NotebookImage,
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
			PodDefaults:  cfg.PodDefaults,
		})
		k8sserving.Register(client.Dispatcher(), k8sserving.Config{
			ClusterName: cfg.ClusterName,
			Namespaces:  cfg.Namespaces,
			Client:      cfg.K8sClient,
			Namespace:   cfg.ServingNamespace,
		})
		k8spipeline.Register(client.Dispatcher(), k8spipeline.Config{
			MasterURL:            cfg.MasterURL,
			Token:                cfg.Token,
			Namespaces:           cfg.Namespaces,
			Client:               cfg.K8sClient,
			Namespace:            cfg.PipelineNamespace,
			WorkerImage:          cfg.PipelineWorkerImage,
			AgentImagePullPolicy: cfg.AgentImagePullPolicy,
			DefaultImage:         cfg.DefaultImage,
			TTLAfterFinished:     cfg.TTLAfterFinished,
			StorageURL:           cfg.StorageURL,
		})
	}

	return &Worker{cfg: cfg, client: client}
}

func (a *Worker) Run(ctx context.Context) error {
	if a.cfg.ClusterName == "" {
		return fmt.Errorf("k8s worker: cluster name is required")
	}
	return a.client.Run(ctx)
}
