package k8sworker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/taskruntime"
	k8snotebook "github.com/piper/piper/pkg/workers/k8s/notebook"
	k8spipeline "github.com/piper/piper/pkg/workers/k8s/pipeline"
	k8sserving "github.com/piper/piper/pkg/workers/k8s/serving"
)

// AgentConfig configures the gRPC connection to the master agent server
// and this worker's identity within the agent registry.
type AgentConfig struct {
	Addr        string // gRPC address of the master agent server, e.g. "master:9090"
	ID          string
	ClusterName string
}

// MasterConfig holds the HTTP connection to the piper master,
// forwarded to K8s Job pods for artifact callbacks.
type MasterConfig struct {
	URL   string
	Token string
}

// K8sConfig groups all Kubernetes cluster options: client, namespaces,
// images, storage, and pod defaults for all workload types.
type K8sConfig struct {
	Client     kubernetes.Interface
	Namespaces []string // namespaces this worker may manage across all workload types

	// Namespace routing per workload type (defaults to first Namespaces entry or "default").
	NotebookNamespace string
	ServingNamespace  string
	PipelineNamespace string

	// Container images.
	NotebookImage        string
	PipelineWorkerImage  string // piper agent image for pipeline Job init containers
	DefaultImage         string // fallback step image
	AgentImagePullPolicy string

	// Storage.
	StorageClass     string
	StorageSize      string
	TTLAfterFinished *int32

	PodDefaults corev1.PodTemplateSpec
}

type Config struct {
	Agent  AgentConfig
	Master MasterConfig
	K8s    K8sConfig
	// StorageURL is the artifact store URL forwarded to pipeline Job pods.
	StorageURL string
	// ResultOutboxDir is the durable directory for unacknowledged pipeline results.
	ResultOutboxDir string
}

type Worker struct {
	cfg              Config
	client           *grpcagent.Client
	notebookObserver *k8snotebook.Worker
	servingObserver  *k8sserving.Worker
	pipelineObserver *k8spipeline.Worker
	resultOutbox     *taskruntime.ResultOutbox
	initErr          error
}

func New(cfg Config) *Worker {
	capabilities := []string{iagent.CapabilityK8s}
	if cfg.K8s.Client != nil {
		capabilities = append(capabilities, iagent.CapabilityNotebook, iagent.CapabilityServing, iagent.CapabilityPipeline)
	}

	client := grpcagent.NewClient(grpcagent.ClientConfig{
		AgentAddr:    cfg.Agent.Addr,
		AgentID:      cfg.Agent.ID,
		Kind:         iagent.KindK8s,
		ClusterName:  cfg.Agent.ClusterName,
		Capabilities: capabilities,
		Runtime:      iagent.RuntimeK8s,
	})
	outboxDir := cfg.ResultOutboxDir
	if outboxDir == "" {
		outboxDir = filepath.Join(os.TempDir(), "piper-result-outbox", cfg.Agent.ID)
	}
	outbox, err := taskruntime.NewResultOutbox(
		outboxDir,
		func(result proto.TaskResult) error {
			return client.SendPush(iagent.MethodPipelineTaskResult, result)
		},
	)
	if err == nil {
		_ = grpcagent.RegisterJSON(client.Dispatcher(), iagent.MethodPipelineResultAck, func(_ context.Context, ack taskruntime.ResultAck) (any, error) {
			return nil, outbox.Ack(ack)
		})
	}

	var notebookObserver *k8snotebook.Worker
	var servingObserver *k8sserving.Worker
	var pipelineObserver *k8spipeline.Worker
	if cfg.K8s.Client != nil {
		notebookObserver = k8snotebook.Register(client.Dispatcher(), k8snotebook.Config{
			WorkerID:     cfg.Agent.ID,
			ClusterName:  cfg.Agent.ClusterName,
			Namespaces:   cfg.K8s.Namespaces,
			Client:       cfg.K8s.Client,
			Namespace:    cfg.K8s.NotebookNamespace,
			Image:        cfg.K8s.NotebookImage,
			StorageClass: cfg.K8s.StorageClass,
			StorageSize:  cfg.K8s.StorageSize,
			PodDefaults:  cfg.K8s.PodDefaults,
			ReportStatus: func(update notebook.WorkerStatusUpdate) error {
				return client.SendPush(iagent.MethodNotebookStatusUpdate, update)
			},
		})
		servingObserver = k8sserving.Register(client.Dispatcher(), k8sserving.Config{
			ClusterName: cfg.Agent.ClusterName,
			Namespaces:  cfg.K8s.Namespaces,
			Client:      cfg.K8s.Client,
			Namespace:   cfg.K8s.ServingNamespace,
			ReportStatus: func(update serving.WorkerStatusUpdate) error {
				return client.SendPush(iagent.MethodServingStatusUpdate, update)
			},
		})
		pipelineObserver = k8spipeline.Register(client.Dispatcher(), k8spipeline.Config{
			WorkerID: cfg.Agent.ID,
			Store: k8spipeline.StoreConfig{
				MasterURL:  cfg.Master.URL,
				Token:      cfg.Master.Token,
				StorageURL: cfg.StorageURL,
			},
			K8s: k8spipeline.K8sConfig{
				Client:               cfg.K8s.Client,
				Namespace:            cfg.K8s.PipelineNamespace,
				Namespaces:           cfg.K8s.Namespaces,
				AgentImage:           cfg.K8s.PipelineWorkerImage,
				AgentImagePullPolicy: cfg.K8s.AgentImagePullPolicy,
				DefaultImage:         cfg.K8s.DefaultImage,
				TTLAfterFinished:     cfg.K8s.TTLAfterFinished,
			},
			ReportResult: func(result proto.TaskResult) error {
				if outbox == nil {
					return fmt.Errorf("result outbox unavailable")
				}
				return outbox.Enqueue(result)
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
		resultOutbox:     outbox,
		initErr:          err,
	}
}

func (a *Worker) Run(ctx context.Context) error {
	if a.initErr != nil {
		return fmt.Errorf("k8s worker result outbox: %w", a.initErr)
	}
	if a.cfg.Agent.ClusterName == "" {
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
	if a.resultOutbox != nil {
		go a.resultOutbox.Run(ctx)
	}
	return a.client.Run(ctx)
}
