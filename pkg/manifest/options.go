package manifest

// SpecOptions holds settings common to a kind (or step) regardless of runtime.
// All kinds use the same type; the scope of application differs per kind.
//
// Applied:
//   - Pipeline step env: injected into the step process environment.
//
// Not yet applied (parsed but ignored):
//   - Timeout: enforced in a future release.
//   - Notebook/Serving env: env injection for these domains is not yet implemented.
type SpecOptions struct {
	Env     []EnvVar `yaml:"env,omitempty"     json:"env,omitempty"`
	Timeout int      `yaml:"timeout,omitempty" json:"timeout,omitempty"` // seconds; not yet enforced
}

type EnvVar struct {
	Name  string `yaml:"name"  json:"name"`
	Value string `yaml:"value" json:"value"`
}
