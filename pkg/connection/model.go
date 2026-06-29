package connection

import "time"

type Type string

const (
	TypeGit      Type = "git"
	TypeRegistry Type = "registry"
)

type Metadata struct {
	ProjectID       string     `json:"-"                        db:"project_id"`
	Name            string     `json:"name"                     db:"name"`
	Type            Type       `json:"type"                     db:"type"`
	Endpoint        string     `json:"endpoint"                 db:"endpoint"`
	Disabled        bool       `json:"disabled"                 db:"disabled"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"   db:"last_used_at"`
	LastTestedAt    *time.Time `json:"last_tested_at,omitempty" db:"last_tested_at"`
	LastTestOK      *bool      `json:"last_test_ok,omitempty"   db:"last_test_ok"`
	LastTestMessage string     `json:"last_test_message"        db:"last_test_message"`
	CreatedAt       time.Time  `json:"created_at"               db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"               db:"updated_at"`
}

// Value holds the decrypted credentials keyed by field name.
// git:      username, token
// registry: username, password
type Value struct {
	Data map[string]string `json:"data"`
}

type Record struct {
	Metadata
}

type CreateRequest struct {
	Name     string            `json:"name"`
	Type     Type              `json:"type"`
	Endpoint string            `json:"endpoint"`
	Data     map[string]string `json:"data"`
}

type RotateRequest struct {
	Data map[string]string `json:"data"`
}

type PatchRequest struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	Endpoint *string `json:"endpoint,omitempty"`
}

type TestRequest struct {
	// Repo is the concrete git URL to test against (required for git connections).
	Repo string `json:"repo,omitempty"`
}

type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
