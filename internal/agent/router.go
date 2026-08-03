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

// AmbiguousInfrastructureError is returned by Select/Reserve when more than
// one candidate agent matches a placement and the caller did not name a
// specific placement.WorkerID to disambiguate. This covers two cases: (1)
// candidates span more than one infrastructure type — baremetal, Docker, and
// Kubernetes workers have materially different execution semantics, and an
// image-less pipeline step, for example, only runs on baremetal; (2)
// candidates share the same infrastructure type but are otherwise distinct
// workers (e.g. two separately managed Kubernetes clusters both tagged
// "k8s") — silently load-balancing across them would deploy to whichever
// happens to be least loaded at that moment, non-deterministically landing
// on a cluster the caller never chose. Either way this is a configuration
// problem, not a transient capacity issue: callers should treat it as
// non-retryable, unlike a plain "no capacity" refusal.
type AmbiguousInfrastructureError struct {
	Kind  WorkloadKind
	Types []string
	Count int
}

func (e *AmbiguousInfrastructureError) Error() string {
	if len(e.Types) > 1 {
		return fmt.Sprintf("ambiguous placement for %s: %d infrastructure types registered (%v) and none was requested; set placement.runtime to disambiguate", e.Kind, len(e.Types), e.Types)
	}
	return fmt.Sprintf("ambiguous placement for %s: %d candidate workers match and none was named; set placement.worker to disambiguate", e.Kind, e.Count)
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

// Select returns the agent for the given placement. An explicit
// placement.WorkerID always wins. Without one, placement must narrow the
// registry down to exactly one candidate (via Infrastructure, Namespace,
// ClusterName, or Labels); if more than one candidate remains, Select
// returns an *AmbiguousInfrastructureError instead of guessing.
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
		// More than one candidate remains even after Infrastructure/Namespace/
		// Labels narrowing. Picking one automatically (e.g. least-loaded)
		// would silently deploy to whichever worker happens to be idle,
		// which is exactly the "random cluster" outcome placement.WorkerID
		// exists to prevent. Require the caller to name one explicitly.
		return nil, &AmbiguousInfrastructureError{Kind: kind, Types: distinctInfrastructures(candidates), Count: len(candidates)}
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
		if len(candidates) > 1 {
			// See the comment on Select's default case: more than one
			// candidate must never be resolved automatically, regardless of
			// whether they share an infrastructure type.
			return nil, &AmbiguousInfrastructureError{Kind: kind, Types: distinctInfrastructures(candidates), Count: len(candidates)}
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
// Returns nil if all bounded candidates are full. Reserve is its only
// caller, and only ever with 0 or 1 candidates now that ambiguous matches
// are rejected before reaching this point — the multi-candidate ranking
// below only matters if a future caller reintroduces automatic selection
// among several agents.
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
