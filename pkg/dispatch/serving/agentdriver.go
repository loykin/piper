package servingdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentDriver struct {
	router    *iagent.Router
	rpc       AgentRPC
	repo      serving.Repository
	masterURL string
}

func NewAgentDriver(router *iagent.Router, rpc AgentRPC, repo serving.Repository, masterURL ...string) *AgentDriver {
	d := &AgentDriver{router: router, rpc: rpc, repo: repo}
	if len(masterURL) > 0 {
		d.masterURL = masterURL[0]
	}
	return d
}

func (d *AgentDriver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	agentInfo, err := d.selectAgent(spec.Spec.Runtime.Worker)
	if err != nil {
		return nil, err
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.deployBareMetal(ctx, agentInfo, spec, art, yamlStr)
	}
	if art.S3URI == "" {
		return nil, fmt.Errorf("agent serving requires S3 artifact storage (configure S3.Bucket or use s3:// model URI)")
	}
	var result struct {
		Endpoint  string `json:"endpoint"`
		Namespace string `json:"namespace"`
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodServingDeploy, map[string]any{
		"yaml":   yamlStr,
		"s3_uri": art.S3URI,
	}, &result); err != nil {
		return nil, fmt.Errorf("serving agent deploy: %w", err)
	}

	return &serving.Service{
		Name:      spec.Metadata.Name,
		Artifact:  artifactLabel(spec),
		Status:    serving.StatusRunning,
		Endpoint:  result.Endpoint,
		Namespace: result.Namespace,
		WorkerID:  agentInfo.ID,
		YAML:      yamlStr,
	}, nil
}

func (d *AgentDriver) Stop(ctx context.Context, svc *serving.Service) error {
	agentInfo, err := d.selectAgent(serviceWorkerID(svc))
	if err != nil {
		return nil
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.stopBareMetal(ctx, agentInfo, svc)
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodServingStop, map[string]any{
		"name":      svc.Name,
		"namespace": svc.Namespace,
	}, nil); err != nil {
		return fmt.Errorf("serving agent stop: %w", err)
	}
	return nil
}

func (d *AgentDriver) Restart(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) error {
	existing, _ := d.repo.Get(ctx, spec.Metadata.Name)
	agentInfo, err := d.selectAgent(serviceWorkerID(existing))
	if err != nil {
		return err
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.restartBareMetal(ctx, agentInfo, spec)
	}
	if art.S3URI == "" {
		return fmt.Errorf("agent serving requires S3 artifact storage (configure S3.Bucket or use s3:// model URI)")
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodServingRestart, map[string]any{
		"yaml":   yamlStr,
		"s3_uri": art.S3URI,
		"name":   spec.Metadata.Name,
	}, nil); err != nil {
		return fmt.Errorf("serving agent restart: %w", err)
	}
	return nil
}

func (d *AgentDriver) deployBareMetal(ctx context.Context, agentInfo *iagent.Info, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	var result struct {
		Endpoint string `json:"endpoint"`
	}
	if err := postServingBareMetalJSON(ctx, agentInfo.Addr+"/deploy", map[string]any{
		"yaml":       yamlStr,
		"local_path": art.LocalPath,
		"s3_uri":     art.S3URI,
		"master_url": d.masterURL,
	}, &result, http.StatusOK); err != nil {
		return nil, fmt.Errorf("serving bare-metal deploy: %w", err)
	}
	return &serving.Service{
		Name:     spec.Metadata.Name,
		Artifact: artifactLabel(spec),
		Status:   serving.StatusRunning,
		Endpoint: result.Endpoint,
		WorkerID: agentInfo.ID,
		YAML:     yamlStr,
	}, nil
}

func (d *AgentDriver) stopBareMetal(ctx context.Context, agentInfo *iagent.Info, svc *serving.Service) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/service/%s", agentInfo.Addr, svc.Name), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("worker returned status %d", resp.StatusCode)
	}
	return nil
}

func (d *AgentDriver) restartBareMetal(ctx context.Context, agentInfo *iagent.Info, spec serving.ModelService) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/service/%s/restart", agentInfo.Addr, spec.Metadata.Name), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("worker returned status %d", resp.StatusCode)
	}
	return nil
}

func postServingBareMetalJSON(ctx context.Context, endpoint string, payload any, result any, okStatuses ...int) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	for _, status := range okStatuses {
		if resp.StatusCode == status {
			if result != nil {
				_ = json.NewDecoder(resp.Body).Decode(result)
			}
			return nil
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("worker returned status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
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
