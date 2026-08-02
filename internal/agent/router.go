package agent

import (
	"fmt"
	"sync"
)

// Router resolves workload placement to the best available live agent.
type Router struct {
	registry *Registry
	defaults map[WorkloadKind]Placement

	loadMu   sync.Mutex
	reserved map[string]int
}

func NewRouter(registry *Registry) *Router {
	return &Router{
		registry: registry,
		defaults: make(map[WorkloadKind]Placement),
		reserved: make(map[string]int),
	}
}

func (r *Router) SetDefault(kind WorkloadKind, placement Placement) {
	r.defaults[kind] = placement
}

// AmbiguousInfrastructureError is returned by Select/Reserve when candidates
// span more than one infrastructure type and the caller did not set
// placement.Infrastructure (driver.placement.runtime in the workload spec)
// to disambiguate. Baremetal, Docker, and Kubernetes workers have materially
// different execution semantics — an image-less pipeline step, for example,
// only runs on baremetal, but nothing in an unset placement says so.
// Silently load-balancing across infrastructure types would route a task to
// an environment it was never configured for, non-deterministically (which
// candidate happens to be least loaded at that moment). This is a
// configuration problem, not a transient capacity issue: callers should
// treat it as non-retryable, unlike a plain "no capacity" refusal.
type AmbiguousInfrastructureError struct {
	Kind  WorkloadKind
	Types []string
}

func (e *AmbiguousInfrastructureError) Error() string {
	return fmt.Sprintf("ambiguous placement for %s: %d infrastructure types registered (%v) and none was requested; set placement.runtime to disambiguate", e.Kind, len(e.Types), e.Types)
}

// distinctInfrastructures returns the set of distinct Info.Infrastructure
// values present among candidates, in first-seen order.
func distinctInfrastructures(candidates []Info) []string {
	seen := make(map[string]bool, len(candidates))
	var out []string
	for _, c := range candidates {
		if !seen[c.Infrastructure] {
			seen[c.Infrastructure] = true
			out = append(out, c.Infrastructure)
		}
	}
	return out
}

// Select returns the best agent for the given placement.
// When multiple candidates match it selects the one with the lowest load
// (reserved/capacity ratio). Unlimited-capacity agents (Capacity==0) are
// always considered available and preferred last over bounded workers.
func (r *Router) Select(kind WorkloadKind, placement Placement) (*Info, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("agent router is not configured")
	}
	if placement.WorkerID != "" {
		a, err := r.registry.Get(placement.WorkerID)
		if err != nil {
			a, err = r.registry.GetByHostname(placement.WorkerID, kind)
			if err != nil {
				return nil, err
			}
		}
		if !hasCapability(a, string(kind)) {
			return nil, fmt.Errorf("agent %q does not support %s", placement.WorkerID, kind)
		}
		if placement.Infrastructure != "" && a.Infrastructure != placement.Infrastructure {
			return nil, fmt.Errorf("agent %q infrastructure %q does not match requested runtime %q", a.ID, a.Infrastructure, placement.Infrastructure)
		}
		if placement.RequireContainer && a.Infrastructure != InfrastructureDocker && a.Infrastructure != InfrastructureK8s {
			return nil, fmt.Errorf("agent %q infrastructure %q cannot execute image-based pipeline", a.ID, a.Infrastructure)
		}
		return a, nil
	}
	if placement.ClusterName == "" && placement.Namespace == "" && len(placement.Labels) == 0 && placement.Infrastructure == "" {
		if def, ok := r.defaults[kind]; ok {
			placement = def
		}
	}
	candidates := r.registry.Candidates(kind, placement)
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("no %s agent available for placement", kind)
	case 1:
		return &candidates[0], nil
	default:
		if placement.Infrastructure == "" {
			if types := distinctInfrastructures(candidates); len(types) > 1 {
				return nil, &AmbiguousInfrastructureError{Kind: kind, Types: types}
			}
		}
		// Multiple candidates, all the same infrastructure type: pick the
		// least-loaded one.
		r.loadMu.Lock()
		best := selectLeastLoaded(candidates, r.reserved)
		r.loadMu.Unlock()
		if best == nil {
			return nil, fmt.Errorf("no %s agent has available capacity", kind)
		}
		return best, nil
	}
}

// Reserve selects an agent and atomically reserves one task slot on it.
func (r *Router) Reserve(kind WorkloadKind, placement Placement) (*Info, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("agent router is not configured")
	}
	if placement.ClusterName == "" && placement.Namespace == "" && len(placement.Labels) == 0 && placement.WorkerID == "" && placement.Infrastructure == "" {
		if def, ok := r.defaults[kind]; ok {
			placement = def
		}
	}

	r.loadMu.Lock()
	defer r.loadMu.Unlock()

	var candidates []Info
	if placement.WorkerID != "" {
		agentInfo, err := r.registry.Get(placement.WorkerID)
		if err != nil {
			agentInfo, err = r.registry.GetByHostname(placement.WorkerID, kind)
			if err != nil {
				return nil, err
			}
		}
		if !hasCapability(agentInfo, string(kind)) {
			return nil, fmt.Errorf("agent %q does not support %s", placement.WorkerID, kind)
		}
		if placement.Infrastructure != "" && agentInfo.Infrastructure != placement.Infrastructure {
			return nil, fmt.Errorf("agent %q infrastructure %q does not match requested runtime %q", agentInfo.ID, agentInfo.Infrastructure, placement.Infrastructure)
		}
		if placement.RequireContainer && agentInfo.Infrastructure != InfrastructureDocker && agentInfo.Infrastructure != InfrastructureK8s {
			return nil, fmt.Errorf("agent %q infrastructure %q cannot execute image-based pipeline", agentInfo.ID, agentInfo.Infrastructure)
		}
		candidates = []Info{*agentInfo}
	} else {
		candidates = r.registry.Candidates(kind, placement)
		if placement.Infrastructure == "" {
			if types := distinctInfrastructures(candidates); len(types) > 1 {
				return nil, &AmbiguousInfrastructureError{Kind: kind, Types: types}
			}
		}
	}

	best := selectLeastLoaded(candidates, r.reserved)
	if best == nil {
		return nil, fmt.Errorf("no %s agent has available capacity", kind)
	}
	r.reserved[best.ID]++
	return best, nil
}

// ReserveAgent reserves one task slot on an already selected run agent.
func (r *Router) ReserveAgent(agentID string, kind WorkloadKind) error {
	if r == nil || r.registry == nil {
		return fmt.Errorf("agent router is not configured")
	}
	r.loadMu.Lock()
	defer r.loadMu.Unlock()

	agentInfo, err := r.registry.Get(agentID)
	if err != nil {
		return err
	}
	if !hasCapability(agentInfo, string(kind)) {
		return fmt.Errorf("agent %q does not support %s", agentID, kind)
	}
	if agentInfo.Capacity > 0 && r.reserved[agentID] >= agentInfo.Capacity {
		return fmt.Errorf("agent %q has no available capacity", agentID)
	}
	r.reserved[agentID]++
	return nil
}

// Release releases one previously reserved task slot.
func (r *Router) Release(agentID string) {
	if r == nil || agentID == "" {
		return
	}
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	if r.reserved[agentID] <= 1 {
		delete(r.reserved, agentID)
		return
	}
	r.reserved[agentID]--
}

// selectLeastLoaded picks the candidate with the lowest load ratio.
// Candidates with Capacity==0 (unlimited, e.g. K8s) are fallback candidates.
// Returns nil if all bounded candidates are full.
func selectLeastLoaded(candidates []Info, reserved map[string]int) *Info {
	var bestBounded *Info
	var bestUnlimited *Info
	bestReserved := 0

	for i := range candidates {
		c := &candidates[i]
		if c.Capacity == 0 {
			if bestUnlimited == nil ||
				reserved[c.ID] < reserved[bestUnlimited.ID] ||
				(reserved[c.ID] == reserved[bestUnlimited.ID] && c.RegisteredAt.Before(bestUnlimited.RegisteredAt)) {
				bestUnlimited = c
			}
			continue
		}
		currentReserved := reserved[c.ID]
		if currentReserved >= c.Capacity {
			continue
		}
		if bestBounded == nil ||
			currentReserved*bestBounded.Capacity < bestReserved*c.Capacity ||
			(currentReserved*bestBounded.Capacity == bestReserved*c.Capacity &&
				c.RegisteredAt.Before(bestBounded.RegisteredAt)) {
			bestBounded = c
			bestReserved = currentReserved
		}
	}
	if bestBounded != nil {
		return bestBounded
	}
	return bestUnlimited
}
