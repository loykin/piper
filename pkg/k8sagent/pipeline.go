package k8sagent

import (
	"context"
	"encoding/json"
	"fmt"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/k8s"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

type pipelineCancelRunRequest struct {
	RunID     string `json:"run_id"`
	Namespace string `json:"namespace,omitempty"`
}

func registerPipelineHandlers(a *Agent) {
	_ = a.dispatcher.Register(iagent.MethodPipelineDispatch, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var task proto.Task
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, err
		}
		return nil, a.dispatchPipeline(ctx, &task)
	})
	_ = a.dispatcher.Register(iagent.MethodPipelineCancelRun, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req pipelineCancelRunRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return nil, a.cancelPipelineRun(ctx, req)
	})
}

func (a *Agent) dispatchPipeline(ctx context.Context, task *proto.Task) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}
	namespace, err := pipelineTaskNamespace(task)
	if err != nil {
		return err
	}
	return a.pipelineLauncher(namespace).Dispatch(ctx, task)
}

func (a *Agent) cancelPipelineRun(ctx context.Context, req pipelineCancelRunRequest) error {
	if req.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	return a.pipelineLauncher(req.Namespace).CancelRun(ctx, req.RunID)
}

func (a *Agent) pipelineLauncher(namespace string) *k8s.Launcher {
	return k8s.NewWithClient(k8s.Config{
		AgentImage:           a.pipelineAgentImage(),
		Namespace:            a.pipelineNamespace(namespace),
		MasterURL:            a.cfg.MasterURL,
		Token:                a.cfg.Token,
		S3Endpoint:           a.cfg.S3Endpoint,
		S3AccessKey:          a.cfg.S3AccessKey,
		S3SecretKey:          a.cfg.S3SecretKey,
		S3Bucket:             a.cfg.S3Bucket,
		S3UseSSL:             a.cfg.S3UseSSL,
		DefaultImage:         a.cfg.DefaultImage,
		AgentImagePullPolicy: a.cfg.AgentImagePullPolicy,
		TTLAfterFinished:     a.cfg.TTLAfterFinished,
	}, a.cfg.K8sClient)
}

func (a *Agent) pipelineNamespace(namespace string) string {
	if namespace != "" {
		return namespace
	}
	if a.cfg.PipelineNamespace != "" {
		return a.cfg.PipelineNamespace
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

func (a *Agent) pipelineAgentImage() string {
	if a.cfg.PipelineAgentImage != "" {
		return a.cfg.PipelineAgentImage
	}
	return "piper/piper:latest"
}
