package credential

import "time"

type Kind string

const (
	KindGeneric Kind = "generic"
	KindGit     Kind = "git"
	KindS3      Kind = "s3"
)

type Metadata struct {
	ProjectID       string     `json:"-"                        db:"project_id"`
	Name            string     `json:"name"                     db:"name"`
	Kind            Kind       `json:"kind"                     db:"kind"`
	Endpoint        string     `json:"endpoint,omitempty"       db:"endpoint"`
	Keys            []string   `json:"keys,omitempty"           db:"-"`
	Disabled        bool       `json:"disabled"                 db:"disabled"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"   db:"last_used_at"`
	LastTestedAt    *time.Time `json:"last_tested_at,omitempty" db:"last_tested_at"`
	LastTestOK      *bool      `json:"last_test_ok,omitempty"   db:"last_test_ok"`
	LastTestMessage string     `json:"last_test_message"        db:"last_test_message"`
	CreatedAt       time.Time  `json:"created_at"               db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"               db:"updated_at"`
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
	Kind     Kind              `json:"kind"`
	Endpoint string            `json:"endpoint,omitempty"`
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
	Repo string `json:"repo,omitempty"`
}

type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
