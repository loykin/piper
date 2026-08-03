package notebookdispatch

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/notebook"
)

type AgentRPC interface {
	SendRPC(ctx context.Context, agentID, method string, payload any, result any) error
}

// EnvResolver resolves manifest.EnvVar entries (including credentialRef) into
// "KEY=value" strings. Implement with (*credential.Store).ResolveEnv.
type EnvResolver func(ctx context.Context, projectID string, env []manifest.EnvVar) ([]string, error)

type AgentDriver struct {
	router      *iagent.Router
	rpc         AgentRPC
	repo        notebook.Repository
	podPolicies iagent.WorkerPodPolicyRepository
	envResolver EnvResolver // optional; nil = no credential resolution
}

// NewAgentDriver constructs a notebook agent driver with an optional pod policy repository.
func NewAgentDriver(router *iagent.Router, rpc AgentRPC, repo notebook.Repository, policies ...iagent.WorkerPodPolicyRepository) *AgentDriver {
	d := &AgentDriver{router: router, rpc: rpc, repo: repo}
	if len(policies) > 0 {
		d.podPolicies = policies[0]
	}
	return d
}

// WithEnvResolver sets the resolver used to expand credentialRef entries in
// spec.Spec.Options.Env before dispatch.
func (d *AgentDriver) WithEnvResolver(r EnvResolver) *AgentDriver {
	d.envResolver = r
	return d
}

func (d *AgentDriver) ProvisionVolume(ctx context.Context, vol *notebook.NotebookVolume, spec notebook.Notebook) error {
	agentInfo, err := d.selectAgent(vol.WorkerID, spec.Spec.Driver.Placement.Runtime)
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
	runtime := spec.Spec.Driver.Placement.Runtime
	agentInfo, err := d.selectAgent(workerID, runtime)
	if err != nil {
		if workerID == "" || spec.WorkerID() != "" {
			return nil, err
		}
		agentInfo, err = d.selectAgent("", runtime)
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

	// Apply per-worker pod policy for K8s workers before dispatch.
	dispatchYAML := yamlStr
	if d.podPolicies != nil && agentInfo.Infrastructure == iagent.InfrastructureK8s {
		if policy, pErr := d.podPolicies.Get(ctx, agentInfo.ID); pErr == nil && policy != nil {
			if merged, mErr := applyPodPolicy(yamlStr, policy.PodTemplate); mErr == nil {
				dispatchYAML = merged
			} else {
				slog.Warn("notebook: pod policy merge failed, using original YAML",
					"worker", agentInfo.ID, "err", mErr)
			}
		}
	}

	// Resolve secret refs in options.env before sending to the worker.
	var resolvedEnv []string
	if d.envResolver != nil && len(spec.Spec.Options.Env) > 0 {
		resolvedEnv, err = d.envResolver(ctx, projectID, spec.Spec.Options.Env)
		if err != nil {
			return nil, fmt.Errorf("notebook env resolution: %w", err)
		}
	}

	var result notebook.WorkerStartResponse
	if err := d.rpc.SendRPC(ctx, agentInfo.ID, iagent.MethodNotebookStart, notebook.WorkerStartRequest{
		ProjectID: projectID,
		YAML:      dispatchYAML,
		WorkDir:   workDir,
		VolumeID:  volumeID,
		Env:       resolvedEnv,
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
	agentInfo, err := d.selectAgent(nb.WorkerID, "")
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
	agentInfo, err := d.selectAgent(vol.WorkerID, "")
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
		agentInfo, err := d.selectAgent(nb.WorkerID, "")
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

// selectAgent picks a notebook-capable worker. When runtime is non-empty
// (an explicit driver.placement.runtime in the workload spec), the selection
// is restricted to workers of that infrastructure — otherwise a mix of
// worker types could satisfy "notebook" capability but disagree with the
// driver config (e.g. spec.driver.docker) the caller actually built,
// producing infrastructure-specific errors (like a missing k8s namespace)
// for a notebook that was never meant to run on k8s.
func (d *AgentDriver) selectAgent(workerID, runtime string) (*iagent.Info, error) {
	if d == nil || d.router == nil || d.rpc == nil {
		return nil, fmt.Errorf("notebook agent driver is not configured")
	}
	placement := iagent.Placement{WorkerID: workerID, Infrastructure: runtime}
	return d.router.Select(iagent.WorkloadNotebook, placement)
}
