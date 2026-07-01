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
	CredentialRef *CredentialRef `yaml:"credentialRef,omitempty" json:"credentialRef,omitempty"`
}

// CredentialRef resolves an env var value from a project generic credential.
type CredentialRef struct {
	Name string `yaml:"name" json:"name"` // credential name
	Key  string `yaml:"key"  json:"key"`  // key within the credential's data map
}
