package notebookdispatch

import (
	"context"
	"fmt"

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
	var result notebook.WorkerProvisionVolumeResponse
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
	workerID := spec.WorkerID()
	if vol != nil && vol.WorkerID != "" {
		workerID = vol.WorkerID
	}
	agentInfo, err := d.selectAgent(workerID)
	if err != nil {
		if workerID == "" || spec.WorkerID() != "" {
			return nil, err
		}
		agentInfo, err = d.selectAgent("")
		if err != nil {
			return nil, err
		}
	}

	workDir := ""
	volumeID := ""
	if vol != nil {
		workDir = vol.WorkDir
		volumeID = vol.ID
		vol.WorkerID = agentInfo.ID
	}
	var result notebook.WorkerStartResponse
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStart, notebook.WorkerStartRequest{
		YAML:     yamlStr,
		WorkDir:  workDir,
		VolumeID: volumeID,
	}, &result); err != nil {
		return nil, fmt.Errorf("notebook agent start: %w", err)
	}
	return &notebook.NotebookServer{
		Name:     spec.Metadata.Name,
		Status:   notebook.StatusStarting,
		Token:    result.Token,
		WorkDir:  result.WorkDir,
		Endpoint: result.Endpoint,
		WorkerID: agentInfo.ID,
	}, nil
}

func (d *AgentDriver) Stop(ctx context.Context, nb *notebook.NotebookServer) error {
	agentInfo, err := d.selectAgent(nb.WorkerID)
	if err != nil {
		return notebook.ErrAgentUnavailable
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
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookDeprovision, map[string]any{
		"volume_id": vol.ID,
	}, nil); err != nil {
		return fmt.Errorf("notebook agent deprovision volume: %w", err)
	}
	return nil
}

func (d *AgentDriver) SyncStatus(ctx context.Context, servers []*notebook.NotebookServer, apply func(name, status string)) error {
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
			// Worker is offline — notebook state is unknown, not "stopped".
			// Status remains as last-known until the worker reconnects and reports.
			continue
		}
		byAgent[agentInfo.ID] = append(byAgent[agentInfo.ID], nb.Name)
	}
	for agentID, names := range byAgent {
		var result notebook.WorkerSyncStatusResponse
		if err := d.rpc.SendRPC(ctx, agentID, iagent.MethodNotebookSyncStatus, notebook.WorkerSyncStatusRequest{Names: names}, &result); err != nil {
			return fmt.Errorf("notebook agent sync status: %w", err)
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
		return nil, fmt.Errorf("notebook agent driver is not configured")
	}
	placement := iagent.Placement{WorkerID: workerID}
	return d.router.Select(iagent.WorkloadNotebook, placement)
}
