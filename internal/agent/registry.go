package agent

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Registry tracks live execution agents. It does not persist workload state.
//
// Agent liveness is connection-driven: agents are added via Register when their
// gRPC stream opens and removed via Remove when it closes. There is no TTL-based
// eviction — the transport layer signals disconnection explicitly.
//
// Pipeline workers (HTTP polling) call Remove explicitly when their heartbeat
// expires, so no Cleanup sweep is needed here either.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*Info
}

func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*Info)}
}

func (r *Registry) Register(info Info) {
	now := time.Now()
	if info.Kind == "" {
		info.Kind = KindBareMetal
	}
	if info.RegisteredAt.IsZero() {
		info.RegisteredAt = now
	}
	info.LastSeen = now

	r.mu.Lock()
	if info.Kind == KindK8s && info.ClusterName != "" {
		for id, existing := range r.agents {
			if id != info.ID && existing.Kind == KindK8s && existing.ClusterName == info.ClusterName {
				delete(r.agents, id)
				slog.Info("event", "type", "agent.replaced", "old_agent_id", id, "new_agent_id", info.ID, "cluster", info.ClusterName)
			}
		}
	}
	r.agents[info.ID] = cloneInfo(info)
	r.mu.Unlock()

	slog.Info("event", "type", "agent.registered", "agent_id", info.ID, "kind", info.Kind, "cluster", info.ClusterName, "capabilities", info.Capabilities)
}

func (r *Registry) Get(id string) (*Info, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %q not available", id)
	}
	return cloneInfo(*a), nil
}

func (r *Registry) GetByHostname(hostname string, kind WorkloadKind) (*Info, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.agents {
		if a.Hostname != hostname || !hasCapability(a, string(kind)) {
			continue
		}
		return cloneInfo(*a), nil
	}
	return nil, fmt.Errorf("agent hostname %q not available", hostname)
}

func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, *cloneInfo(*a))
	}
	return out
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.agents, id)
	r.mu.Unlock()
	slog.Info("event", "type", "agent.removed", "agent_id", id)
}

func (r *Registry) Candidates(kind WorkloadKind, p Placement) []Info {
	capability := string(kind)
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Info, 0, len(r.agents))
	for _, a := range r.agents {
		if !hasCapability(a, capability) {
			continue
		}
		if p.ClusterName != "" && a.ClusterName != p.ClusterName {
			continue
		}
		if p.Namespace != "" && len(a.Namespaces) > 0 && !slices.Contains(a.Namespaces, p.Namespace) {
			continue
		}
		if !labelsMatch(a.Labels, p.Labels) {
			continue
		}
		out = append(out, *cloneInfo(*a))
	}
	return out
}

func hasCapability(a *Info, capability string) bool {
	return slices.Contains(a.Capabilities, capability)
}

func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func cloneInfo(info Info) *Info {
	cp := info
	cp.GPUs = slices.Clone(info.GPUs)
	cp.Capabilities = slices.Clone(info.Capabilities)
	cp.Namespaces = slices.Clone(info.Namespaces)
	if info.Labels != nil {
		cp.Labels = make(map[string]string, len(info.Labels))
		for k, v := range info.Labels {
			cp.Labels[k] = v
		}
	}
	return &cp
}
