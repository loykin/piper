package secret

import "time"

type Type string

const (
	TypeGit Type = "git"
	TypeEnv Type = "env"
)

type Provider string

const (
	ProviderPiperManaged Provider = "piper-managed"
)

type Metadata struct {
	ProjectID  string     `json:"-" db:"project_id"`
	Name       string     `json:"name" db:"name"`
	Type       Type       `json:"type" db:"type"`
	Provider   Provider   `json:"provider" db:"provider"`
	Keys       []string   `json:"keys" db:"-"`
	Disabled   bool       `json:"disabled" db:"disabled"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
}

type Value struct {
	Data map[string]string `json:"data"`
}

type Record struct {
	Metadata
	KeysJSON string `db:"keys_json"`
}

type CreateRequest struct {
	Name     string            `json:"name"`
	Type     Type              `json:"type,omitempty"` // informational; defaults to TypeEnv
	Provider Provider          `json:"provider"`
	Data     map[string]string `json:"data"`
}

type RotateRequest struct {
	Data map[string]string `json:"data"`
}

type PatchRequest struct {
	Enabled *bool `json:"enabled"`
}

// Ref currently points to piper-managed secrets by name. External providers will
// need persisted ref metadata, such as a future ref_json column, before use.
type Ref struct {
	Name string `yaml:"name" json:"name,omitempty"`
}
