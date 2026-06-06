// Package agent contains the master-side directory of connected worker
// transport endpoints. An agent is not a separate workload owner or source of
// workload state; it is the connection-scoped representation of a Worker used
// for routing RPCs and tunnels.
package agent

import "time"

const (
	KindBareMetal = "baremetal"
	KindK8s       = "k8s"

	CapabilityPipeline = "pipeline"
	CapabilityNotebook = "notebook"
	CapabilityServing  = "serving"
	CapabilityK8s      = "k8s"
	CapabilityTunnel   = "tunnel"
)

// Info describes a connected worker endpoint registered with the server.
// It is connection/runtime metadata only, never workload state.
type Info struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Mode         string            `json:"mode,omitempty"`
	Addr         string            `json:"addr,omitempty"`
	Hostname     string            `json:"hostname,omitempty"`
	GPUs         []string          `json:"gpus,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	ClusterName  string            `json:"cluster_name,omitempty"`
	Namespaces   []string          `json:"namespaces,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
	LastSeen     time.Time         `json:"last_seen"`
}

// Placement is the user-facing "where should this workload run" selector.
type Placement struct {
	WorkerID    string            `json:"worker_id,omitempty"`
	ClusterName string            `json:"cluster_name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type WorkloadKind string

const (
	WorkloadPipeline WorkloadKind = "pipeline"
	WorkloadNotebook WorkloadKind = "notebook"
	WorkloadServing  WorkloadKind = "serving"
)
