package notebookdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/pkg/notebook"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

type AgentDriver struct {
	router *iagent.Router
	rpc    AgentRPC
	repo   notebook.Repository
}

func NewAgentDriver(router *iagent.Router, rpc AgentRPC, repo ...notebook.Repository) *AgentDriver {
	d := &AgentDriver{router: router, rpc: rpc}
	if len(repo) > 0 {
		d.repo = repo[0]
	}
	return d
}

func (d *AgentDriver) ProvisionVolume(ctx context.Context, vol *notebook.NotebookVolume, storageSize string) error {
	agentInfo, err := d.selectAgent(vol.WorkerID)
	if err != nil {
		return err
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.provisionBareMetalVolume(ctx, agentInfo, vol)
	}
	var result struct {
		WorkDir string `json:"work_dir"`
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookProvisionVolume, map[string]any{
		"volume_id":    vol.ID,
		"storage_size": storageSize,
	}, &result); err != nil {
		return fmt.Errorf("notebook agent provision volume: %w", err)
	}
	vol.WorkDir = result.WorkDir
	vol.WorkerID = agentInfo.ID
	return nil
}

func (d *AgentDriver) Start(ctx context.Context, spec notebook.NotebookServerSpec, vol *notebook.NotebookVolume, yamlStr string) (*notebook.NotebookServer, error) {
	workerID := spec.Spec.Worker
	if vol != nil && vol.WorkerID != "" {
		workerID = vol.WorkerID
	}
	agentInfo, err := d.selectAgent(workerID)
	if err != nil {
		return nil, err
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.startBareMetal(ctx, agentInfo, spec, vol, yamlStr)
	}

	workDir := ""
	volumeID := ""
	if vol != nil {
		workDir = vol.WorkDir
		volumeID = vol.ID
	}
	var result struct {
		Token    string `json:"token"`
		WorkDir  string `json:"work_dir"`
		Endpoint string `json:"endpoint"`
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStart, map[string]any{
		"yaml":      yamlStr,
		"work_dir":  workDir,
		"volume_id": volumeID,
	}, &result); err != nil {
		return nil, fmt.Errorf("notebook agent start: %w", err)
	}
	endpoint := result.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("tunnel://%s/nb/%s", agentInfo.ID, spec.Metadata.Name)
	}
	return &notebook.NotebookServer{
		Name:     spec.Metadata.Name,
		Status:   notebook.StatusStarting,
		Token:    result.Token,
		WorkDir:  result.WorkDir,
		Endpoint: endpoint,
		WorkerID: agentInfo.ID,
	}, nil
}

func (d *AgentDriver) Stop(ctx context.Context, nb *notebook.NotebookServer) error {
	agentInfo, err := d.selectAgent(nb.WorkerID)
	if err != nil {
		return nil
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.stopBareMetal(ctx, agentInfo, nb)
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStop, map[string]any{
		"name": nb.Name,
	}, nil); err != nil {
		return fmt.Errorf("notebook agent stop: %w", err)
	}
	return nil
}

func (d *AgentDriver) DeprovisionVolume(ctx context.Context, vol *notebook.NotebookVolume) error {
	agentInfo, err := d.selectAgent(vol.WorkerID)
	if err != nil {
		return nil
	}
	if agentInfo.Kind == iagent.KindBareMetal {
		return d.deprovisionBareMetalVolume(ctx, agentInfo, vol)
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookDeprovision, map[string]any{
		"volume_id": vol.ID,
	}, nil); err != nil {
		return fmt.Errorf("notebook agent deprovision volume: %w", err)
	}
	return nil
}

func (d *AgentDriver) provisionBareMetalVolume(ctx context.Context, agentInfo *iagent.Info, vol *notebook.NotebookVolume) error {
	var result struct {
		WorkDir string `json:"work_dir"`
	}
	if err := postBareMetalJSON(ctx, agentInfo.Addr+"/volume", map[string]any{"volume_id": vol.ID}, &result, http.StatusOK); err != nil {
		return fmt.Errorf("notebook bare-metal provision volume: %w", err)
	}
	vol.WorkDir = result.WorkDir
	vol.WorkerID = agentInfo.ID
	return nil
}

func (d *AgentDriver) startBareMetal(ctx context.Context, agentInfo *iagent.Info, spec notebook.NotebookServerSpec, vol *notebook.NotebookVolume, yamlStr string) (*notebook.NotebookServer, error) {
	workDir := ""
	volumeID := ""
	if vol != nil {
		workDir = vol.WorkDir
		volumeID = vol.ID
	}
	var result struct {
		Token   string `json:"token"`
		WorkDir string `json:"work_dir"`
	}
	if err := postBareMetalJSON(ctx, agentInfo.Addr+"/start", map[string]any{
		"yaml":      yamlStr,
		"work_dir":  workDir,
		"volume_id": volumeID,
	}, &result, http.StatusOK, http.StatusAccepted); err != nil {
		return nil, fmt.Errorf("notebook bare-metal start: %w", err)
	}
	return &notebook.NotebookServer{
		Name:     spec.Metadata.Name,
		Status:   notebook.StatusStarting,
		Token:    result.Token,
		WorkDir:  result.WorkDir,
		WorkerID: agentInfo.ID,
	}, nil
}

func (d *AgentDriver) stopBareMetal(ctx context.Context, agentInfo *iagent.Info, nb *notebook.NotebookServer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/notebook/%s", agentInfo.Addr, nb.Name), nil)
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

func (d *AgentDriver) deprovisionBareMetalVolume(ctx context.Context, agentInfo *iagent.Info, vol *notebook.NotebookVolume) error {
	if vol.WorkDir == "" {
		return nil
	}
	u := fmt.Sprintf("%s/volume/%s?work_dir=%s", agentInfo.Addr, vol.ID, url.QueryEscape(vol.WorkDir))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("worker returned status %d", resp.StatusCode)
	}
	return nil
}

func postBareMetalJSON(ctx context.Context, endpoint string, payload any, result any, okStatuses ...int) error {
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

func (d *AgentDriver) SyncStatus(ctx context.Context, servers []*notebook.NotebookServer) error {
	if d == nil || d.repo == nil {
		return nil
	}
	byAgent := make(map[string][]string)
	for _, nb := range servers {
		if nb == nil {
			continue
		}
		agentInfo, err := d.selectAgent(nb.WorkerID)
		if err != nil {
			continue
		}
		byAgent[agentInfo.ID] = append(byAgent[agentInfo.ID], nb.Name)
	}
	for agentID, names := range byAgent {
		var result struct {
			Statuses map[string]string `json:"statuses"`
		}
		if err := d.rpc.SendRPC(ctx, agentID, iagent.MethodNotebookSyncStatus, map[string]any{
			"names": names,
		}, &result); err != nil {
			return fmt.Errorf("notebook agent sync status: %w", err)
		}
		for name, status := range result.Statuses {
			if status != "" {
				_ = d.repo.SetStatus(ctx, name, status)
			}
		}
	}
	return nil
}

func (d *AgentDriver) selectAgent(workerID string) (*iagent.Info, error) {
	if d == nil || d.router == nil || d.rpc == nil {
		return nil, fmt.Errorf("notebook agent driver is not configured")
	}
	placement := iagent.Placement{WorkerID: workerID}
	return d.router.Select(iagent.WorkloadNotebook, placement)
}
