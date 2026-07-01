package project

import "time"

const DefaultID = "default"

// SystemID is the reserved project that owns system-scoped resources such as
// artifact-storage credentials. It is seeded at startup, cannot be created or
// deleted through the API, and is hidden from project listings.
const SystemID = "system"

// Reserved reports whether id is a reserved system project that users may not
// create or delete through the API.
func Reserved(id string) bool { return id == SystemID }

type Project struct {
	ID          string    `json:"id"          db:"id"`
	Name        string    `json:"name"        db:"name"`
	Description string    `json:"description" db:"description"`
	CreatedAt   time.Time `json:"created_at"  db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"  db:"updated_at"`
}
