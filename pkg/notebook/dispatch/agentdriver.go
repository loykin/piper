package notebookdispatch

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

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

func (d *AgentDriver) ProvisionVolume(ctx context.Context, vol *notebook.NotebookVolume, spec notebook.Notebook) error {
	agentInfo, err := d.selectAgent(vol.WorkerID)
	if err != nil {
		return err
	}
	var result notebook.WorkerProvisionVolumeResponse
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookProvisionVolume, map[string]any{
		"volume_id":     vol.ID,
		"namespace":     spec.K8sNamespace(),
		"storage_size":  spec.StorageSize(),
		"storage_class": spec.StorageClass(),
	}, &result); err != nil {
		return fmt.Errorf("notebook agent provision volume: %w", err)
	}
	vol.WorkDir = result.WorkDir
	vol.WorkerID = agentInfo.ID
	return nil
}

func (d *AgentDriver) Start(ctx context.Context, spec notebook.Notebook, vol *notebook.NotebookVolume, yamlStr string) (*notebook.NotebookServer, error) {
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
	projectID := spec.Metadata.ProjectID
	if vol != nil {
		workDir = vol.WorkDir
		volumeID = vol.ID
		if projectID == "" {
			projectID = vol.ProjectID
		}
		vol.WorkerID = agentInfo.ID
	}
	var result notebook.WorkerStartResponse
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStart, notebook.WorkerStartRequest{
		ProjectID: projectID,
		YAML:      yamlStr,
		WorkDir:   workDir,
		VolumeID:  volumeID,
	}, &result); err != nil {
		return nil, fmt.Errorf("notebook agent start: %w", err)
	}
	return &notebook.NotebookServer{
		ProjectID: projectID,
		Name:      spec.Metadata.Name,
		Status:    notebook.StatusStarting,
		Token:     result.Token,
		WorkDir:   result.WorkDir,
		Endpoint:  result.Endpoint,
		WorkerID:  agentInfo.ID,
	}, nil
}

func (d *AgentDriver) Stop(ctx context.Context, nb *notebook.NotebookServer) error {
	agentInfo, err := d.selectAgent(nb.WorkerID)
	if err != nil {
		return notebook.ErrAgentUnavailable
	}
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStop, notebook.WorkerStopRequest{
		ProjectID: nb.ProjectID,
		Name:      nb.Name,
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

func (d *AgentDriver) SyncStatus(ctx context.Context, servers []*notebook.NotebookServer, apply func(projectID, name, status string)) error {
	if d == nil || d.repo == nil {
		return nil
	}
	byAgent := make(map[string][]notebook.WorkerSyncStatusTarget)
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
		byAgent[agentInfo.ID] = append(byAgent[agentInfo.ID], notebook.WorkerSyncStatusTarget{
			ProjectID: nb.ProjectID,
			Name:      nb.Name,
			Port:      notebookEndpointPort(nb.Endpoint),
		})
	}
	for agentID, targets := range byAgent {
		var result notebook.WorkerSyncStatusResponse
		if err := d.rpc.SendRPC(ctx, agentID, iagent.MethodNotebookSyncStatus, notebook.WorkerSyncStatusRequest{Targets: targets}, &result); err != nil {
			return fmt.Errorf("notebook agent sync status: %w", err)
		}
		for compositeKey, status := range result.Statuses {
			if status == "" {
				continue
			}
			projectID, name, ok := strings.Cut(compositeKey, ":")
			if !ok || projectID == "" || name == "" {
				continue
			}
			apply(projectID, name, status)
		}
	}
	return nil
}

func notebookEndpointPort(endpoint string) int {
	u, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}
	target := u.Query().Get("target")
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func (d *AgentDriver) selectAgent(workerID string) (*iagent.Info, error) {
	if d == nil || d.router == nil || d.rpc == nil {
		return nil, fmt.Errorf("notebook agent driver is not configured")
	}
	placement := iagent.Placement{WorkerID: workerID}
	return d.router.Select(iagent.WorkloadNotebook, placement)
}
