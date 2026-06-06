package k8sworker

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/serving"
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
	cfg              Config
	client           *grpcagent.Client
	notebookObserver *k8snotebook.Worker
	servingObserver  *k8sserving.Worker
	pipelineObserver *k8spipeline.Worker
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

	var notebookObserver *k8snotebook.Worker
	var servingObserver *k8sserving.Worker
	var pipelineObserver *k8spipeline.Worker
	if cfg.K8sClient != nil {
		notebookObserver = k8snotebook.Register(client.Dispatcher(), k8snotebook.Config{
			AgentID:      cfg.ID,
			ClusterName:  cfg.ClusterName,
			Namespaces:   cfg.Namespaces,
			Client:       cfg.K8sClient,
			Namespace:    cfg.NotebookNamespace,
			Image:        cfg.NotebookImage,
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
			PodDefaults:  cfg.PodDefaults,
			ReportStatus: func(update notebook.WorkerStatusUpdate) error {
				return client.SendPush(iagent.MethodNotebookStatusUpdate, update)
			},
		})
		servingObserver = k8sserving.Register(client.Dispatcher(), k8sserving.Config{
			ClusterName: cfg.ClusterName,
			Namespaces:  cfg.Namespaces,
			Client:      cfg.K8sClient,
			Namespace:   cfg.ServingNamespace,
			ReportStatus: func(update serving.WorkerStatusUpdate) error {
				return client.SendPush(iagent.MethodServingStatusUpdate, update)
			},
		})
		pipelineObserver = k8spipeline.Register(client.Dispatcher(), k8spipeline.Config{
			AgentID:              cfg.ID,
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
			ReportResult: func(result proto.TaskResult) error {
				return client.SendPush(iagent.MethodPipelineTaskResult, result)
			},
			RenewLeases: func(taskIDs []string) error {
				return client.SendPush(iagent.MethodPipelineLeaseRenew, map[string]any{"task_ids": taskIDs})
			},
		})
	}

	return &Worker{
		cfg: cfg, client: client,
		notebookObserver: notebookObserver,
		servingObserver:  servingObserver,
		pipelineObserver: pipelineObserver,
	}
}

func (a *Worker) Run(ctx context.Context) error {
	if a.cfg.ClusterName == "" {
		return fmt.Errorf("k8s worker: cluster name is required")
	}
	if a.notebookObserver != nil {
		go a.notebookObserver.Observe(ctx)
	}
	if a.servingObserver != nil {
		go a.servingObserver.Observe(ctx)
	}
	if a.pipelineObserver != nil {
		go a.pipelineObserver.Observe(ctx)
	}
	return a.client.Run(ctx)
}
