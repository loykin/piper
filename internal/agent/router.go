package agent

import "fmt"

// Router resolves workload placement to exactly one live agent.
type Router struct {
	registry *Registry
	defaults map[WorkloadKind]Placement
}

func NewRouter(registry *Registry) *Router {
	return &Router{registry: registry, defaults: make(map[WorkloadKind]Placement)}
}

func (r *Router) SetDefault(kind WorkloadKind, placement Placement) {
	r.defaults[kind] = placement
}

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
		return a, nil
	}
	if placement.ClusterName == "" && placement.Namespace == "" && len(placement.Labels) == 0 {
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
		return nil, fmt.Errorf("multiple %s agents match placement; specify worker or cluster", kind)
	}
}
