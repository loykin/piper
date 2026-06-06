package pipelineworker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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
	StorageURL   string
	AgentID      string
	ReportResult func(proto.TaskResult) error
	RenewLeases  func([]string) error
}

type Worker struct {
	cfg       Config
	mu        sync.Mutex
	launchers map[string]*k8s.Launcher
}

func New(cfg Config) *Worker {
	w := &Worker{cfg: cfg, launchers: make(map[string]*k8s.Launcher)}
	w.pipelineLauncher(cfg.Namespace)
	for _, namespace := range cfg.Namespaces {
		w.pipelineLauncher(namespace)
	}
	return w
}

func Register(dispatcher Dispatcher, cfg Config) *Worker {
	w := New(cfg)
	w.register(dispatcher)
	return w
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
	namespace = a.pipelineNamespace(namespace)
	a.mu.Lock()
	defer a.mu.Unlock()
	if launcher := a.launchers[namespace]; launcher != nil {
		return launcher
	}
	launcher := k8s.NewWithClient(k8s.Config{
		AgentImage:           a.pipelineWorkerImage(),
		Namespace:            namespace,
		MasterURL:            a.cfg.MasterURL,
		Token:                a.cfg.Token,
		StorageURL:           a.cfg.StorageURL,
		DefaultImage:         a.cfg.DefaultImage,
		AgentImagePullPolicy: a.cfg.AgentImagePullPolicy,
		TTLAfterFinished:     a.cfg.TTLAfterFinished,
	}, a.cfg.Client)
	a.launchers[namespace] = launcher
	return launcher
}

func (a *Worker) Observe(ctx context.Context) {
	if a.cfg.ReportResult == nil && a.cfg.RenewLeases == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		a.observeOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Worker) observeOnce(ctx context.Context) {
	a.mu.Lock()
	launchers := make([]*k8s.Launcher, 0, len(a.launchers))
	for _, launcher := range a.launchers {
		launchers = append(launchers, launcher)
	}
	a.mu.Unlock()
	for _, launcher := range launchers {
		launcher.RecoverJobs(ctx, a.cfg.AgentID)
		if a.cfg.RenewLeases != nil {
			taskIDs := launcher.ActiveTaskIDs()
			if len(taskIDs) > 0 {
				_ = a.cfg.RenewLeases(taskIDs)
			}
		}
		if a.cfg.ReportResult != nil {
			launcher.ReconcileJobs(ctx, func(_ context.Context, result proto.TaskResult) error {
				result.WorkerID = a.cfg.AgentID
				return a.cfg.ReportResult(result)
			})
		}
	}
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
