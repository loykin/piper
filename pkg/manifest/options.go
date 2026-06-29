package manifest

// SpecOptions holds settings common to a kind (or step) regardless of runtime.
// All kinds use the same type; the scope of application differs per kind.
//
// Applied:
//   - Pipeline step env: injected into the step process environment.
//   - Notebook env: injected into the notebook server process/container.
//   - Serving env: injected into the serving deployment process/container.
//
// Not yet enforced:
//   - Timeout: enforced in a future release.
type SpecOptions struct {
	Env     []EnvVar `yaml:"env,omitempty"     json:"env,omitempty"`
	Timeout int      `yaml:"timeout,omitempty" json:"timeout,omitempty"` // seconds; not yet enforced
}

// EnvVar is a single environment variable. Exactly one of Value or ValueFrom must
// be set.
type EnvVar struct {
	Name      string        `yaml:"name"                json:"name"`
	Value     string        `yaml:"value,omitempty"     json:"value,omitempty"`
	ValueFrom *EnvVarSource `yaml:"valueFrom,omitempty" json:"valueFrom,omitempty"`
}

// EnvVarSource selects the source for an EnvVar value.
type EnvVarSource struct {
	SecretKeyRef *SecretKeyRef `yaml:"secretKeyRef,omitempty" json:"secretKeyRef,omitempty"`
}

// SecretKeyRef resolves an env var value from a project secret.
type SecretKeyRef struct {
	Name string `yaml:"name" json:"name"` // secret name
	Key  string `yaml:"key"  json:"key"`  // key within the secret's data map
}
