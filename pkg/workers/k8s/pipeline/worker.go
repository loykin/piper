package pipelineworker

import (
	"context"
	"encoding/json"
	"fmt"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/k8s"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

type Config struct {
	MasterURL            string
	Token                string
	Namespaces           []string
	Client               kubernetes.Interface
	Namespace            string
	WorkerImage          string
	AgentImagePullPolicy string
	DefaultImage         string
	TTLAfterFinished     *int32
	// StorageURL selects the artifact store backend passed to K8s Job agents.
	StorageURL string
}

type Worker struct {
	cfg Config
}

func New(cfg Config) *Worker {
	return &Worker{cfg: cfg}
}

func Register(dispatcher Dispatcher, cfg Config) {
	New(cfg).register(dispatcher)
}

type pipelineCancelRunRequest struct {
	RunID     string `json:"run_id"`
	Namespace string `json:"namespace,omitempty"`
}

func (a *Worker) register(dispatcher Dispatcher) {
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodPipelineDispatch, func(ctx context.Context, task proto.Task) (any, error) {
		return nil, a.dispatchPipeline(ctx, &task)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodPipelineCancelRun, func(ctx context.Context, req pipelineCancelRunRequest) (any, error) {
		return nil, a.cancelPipelineRun(ctx, req)
	})
}

func (a *Worker) dispatchPipeline(ctx context.Context, task *proto.Task) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}
	namespace, err := pipelineTaskNamespace(task)
	if err != nil {
		return err
	}
	return a.pipelineLauncher(namespace).Dispatch(ctx, task)
}

func (a *Worker) cancelPipelineRun(ctx context.Context, req pipelineCancelRunRequest) error {
	if req.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	return a.pipelineLauncher(req.Namespace).CancelRun(ctx, req.RunID)
}

func (a *Worker) pipelineLauncher(namespace string) *k8s.Launcher {
	return k8s.NewWithClient(k8s.Config{
		AgentImage:           a.pipelineWorkerImage(),
		Namespace:            a.pipelineNamespace(namespace),
		MasterURL:            a.cfg.MasterURL,
		Token:                a.cfg.Token,
		StorageURL:           a.cfg.StorageURL,
		DefaultImage:         a.cfg.DefaultImage,
		AgentImagePullPolicy: a.cfg.AgentImagePullPolicy,
		TTLAfterFinished:     a.cfg.TTLAfterFinished,
	}, a.cfg.Client)
}

func (a *Worker) pipelineNamespace(namespace string) string {
	if namespace != "" {
		return namespace
	}
	if a.cfg.Namespace != "" {
		return a.cfg.Namespace
	}
	if len(a.cfg.Namespaces) > 0 && a.cfg.Namespaces[0] != "" {
		return a.cfg.Namespaces[0]
	}
	return "default"
}

func pipelineTaskNamespace(task *proto.Task) (string, error) {
	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return "", fmt.Errorf("unmarshal pipeline: %w", err)
	}
	return pl.Spec.Placement.Namespace, nil
}

func (a *Worker) pipelineWorkerImage() string {
	if a.cfg.WorkerImage != "" {
		return a.cfg.WorkerImage
	}
	return "piper/piper:latest"
}
