// Package agent contains the master-side directory of connected worker
// transport endpoints. An agent is not a separate workload owner or source of
// workload state; it is the connection-scoped representation of a Worker used
// for routing RPCs and tunnels.
package agent

import "time"

const (
	InfrastructureBaremetal = "baremetal"
	InfrastructureDocker    = "docker"
	InfrastructureK8s       = "k8s"

	CapabilityPipeline = "pipeline"
	CapabilityNotebook = "notebook"
	CapabilityServing  = "serving"
)

// Info describes a connected worker endpoint registered with the server.
// It is connection/runtime metadata only, never workload state.
type Info struct {
	ID             string            `json:"id"`
	Infrastructure string            `json:"infrastructure"`
	Addr           string            `json:"addr,omitempty"`
	Hostname       string            `json:"hostname,omitempty"`
	Capabilities   []string          `json:"capabilities,omitempty"`
	ClusterName    string            `json:"cluster_name,omitempty"`
	Namespaces     []string          `json:"namespaces,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	// Capacity is the maximum number of concurrent tasks this worker accepts.
	// 0 means unlimited (used by K8s workers where K8s itself limits parallelism).
	Capacity     int       `json:"capacity,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
}

// Placement is the user-facing "where should this workload run" selector.
type Placement struct {
	WorkerID    string            `json:"worker_id,omitempty"`
	ClusterName string            `json:"cluster_name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	// RequireContainer excludes baremetal workers for pipelines that declare an image.
	RequireContainer bool `json:"require_container,omitempty"`
}

type WorkloadKind string

const (
	WorkloadPipeline WorkloadKind = "pipeline"
	WorkloadNotebook WorkloadKind = "notebook"
	WorkloadServing  WorkloadKind = "serving"
)

// BusyErrorMarker is embedded in the error message so that the master-side
// AgentBackend can identify a capacity refusal even after gRPC serialises it
// to a plain string.
const BusyErrorMarker = "[piper:worker_busy]"

// BusyError is returned by a worker when it cannot accept a dispatch
// because it is at capacity. The caller should treat this as a retryable
// dispatch failure and not count it as a step retry.
type BusyError struct {
	Reason string
}

func (e *BusyError) Error() string {
	if e.Reason != "" {
		return BusyErrorMarker + " " + e.Reason
	}
	return BusyErrorMarker
}
