package tunnelproxy

import (
	"fmt"
	"net/http"
	"sync"
)

// PolicyContext carries the request-scoped values a backend policy may need.
type PolicyContext struct {
	Request     *http.Request
	Name        string
	Token       string
	Host        string
	Scheme      string
	ProxyPrefix string
}

// PolicyBuilder constructs a policy for a request-scoped context.
type PolicyBuilder func(PolicyContext) Policy

// Registry stores named policy builders so backends can register their own
// proxy adaptation rules without sharing request plumbing.
type Registry struct {
	mu       sync.RWMutex
	builders map[string]PolicyBuilder
}

var defaultRegistry = NewRegistry()

// NewRegistry creates an empty policy registry.
func NewRegistry() *Registry {
	return &Registry{builders: make(map[string]PolicyBuilder)}
}

// Register installs a policy builder under the given name.
func (r *Registry) Register(name string, builder PolicyBuilder) {
	if r == nil || name == "" || builder == nil {
		return
	}
	r.mu.Lock()
	if r.builders == nil {
		r.builders = make(map[string]PolicyBuilder)
	}
	r.builders[name] = builder
	r.mu.Unlock()
}

// Build constructs a policy using the registered builder for name.
func (r *Registry) Build(name string, ctx PolicyContext) (Policy, error) {
	if r == nil {
		return nil, fmt.Errorf("policy registry is nil")
	}
	r.mu.RLock()
	builder, ok := r.builders[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("policy %q is not registered", name)
	}
	return builder(ctx), nil
}

// RegisterPolicy registers a builder in the default registry.
func RegisterPolicy(name string, builder PolicyBuilder) {
	defaultRegistry.Register(name, builder)
}

// BuildPolicy constructs a policy from the default registry.
func BuildPolicy(name string, ctx PolicyContext) (Policy, error) {
	return defaultRegistry.Build(name, ctx)
}
