package servingdispatch

import (
	"context"
	"fmt"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentDriver struct {
	router *iagent.Router
	rpc    AgentRPC
	repo   serving.Repository
}

func NewAgentDriver(router *iagent.Router, rpc AgentRPC, repo serving.Repository) *AgentDriver {
	return &AgentDriver{router: router, rpc: rpc, repo: repo}
}

func (d *AgentDriver) ArtifactTarget() artifact.Target { return artifact.TargetS3 }

func (d *AgentDriver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	agentInfo, err := d.selectAgent(spec.Spec.Driver.Placement.Worker)
	if err != nil {
		return nil, err
	}
	var result struct {
		Endpoint string `json:"endpoint"`
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodServingDeploy, map[string]any{
		"yaml":       yamlStr,
		"local_path": art.LocalPath,
		"s3_uri":     art.S3URI,
	}, &result); err != nil {
		return nil, fmt.Errorf("serving agent deploy: %w", err)
	}
	return &serving.Service{
		Name:     spec.Metadata.Name,
		Artifact: artifactLabel(spec),
		Status:   serving.StatusStarting,
		Endpoint: result.Endpoint,
		WorkerID: agentInfo.ID,
		YAML:     yamlStr,
	}, nil
}

func (d *AgentDriver) Stop(ctx context.Context, svc *serving.Service) error {
	agentInfo, err := d.selectAgent(serviceWorkerID(svc))
	if err != nil {
		return err
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodServingStop, map[string]any{
		"name":      svc.Name,
		"namespace": svc.Namespace,
	}, nil); err != nil {
		return fmt.Errorf("serving agent stop: %w", err)
	}
	return nil
}

func (d *AgentDriver) SyncStatus(ctx context.Context, services []*serving.Service, apply func(name, status string)) error {
	byAgent := make(map[string][]serving.WorkerSyncStatusTarget)
	for _, svc := range services {
		if svc == nil {
			continue
		}
		agentInfo, err := d.selectAgent(svc.WorkerID)
		if err != nil {
			continue
		}
		byAgent[agentInfo.ID] = append(byAgent[agentInfo.ID], serving.WorkerSyncStatusTarget{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		})
	}
	for agentID, targets := range byAgent {
		var result serving.WorkerSyncStatusResponse
		if err := d.rpc.SendRPC(ctx, agentID, iagent.MethodServingSyncStatus, serving.WorkerSyncStatusRequest{Services: targets}, &result); err != nil {
			return fmt.Errorf("serving agent sync status: %w", err)
		}
		for name, status := range result.Statuses {
			if status != "" {
				apply(name, status)
			}
		}
	}
	return nil
}

func (d *AgentDriver) selectAgent(workerID string) (*iagent.Info, error) {
	if d == nil || d.router == nil || d.rpc == nil {
		return nil, fmt.Errorf("serving agent driver is not configured")
	}
	return d.router.Select(iagent.WorkloadServing, iagent.Placement{WorkerID: workerID})
}

func serviceWorkerID(svc *serving.Service) string {
	if svc == nil {
		return ""
	}
	return svc.WorkerID
}

func artifactLabel(svc serving.ModelService) string {
	if svc.Spec.Model.FromArtifact != nil {
		return svc.Spec.Model.FromArtifact.Step + "/" + svc.Spec.Model.FromArtifact.Artifact
	}
	if svc.Spec.Model.FromURI != "" {
		return svc.Spec.Model.FromURI
	}
	return ""
}
