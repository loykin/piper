package notebook

import "time"

const (
	StatusProvisioning = "provisioning" // volume being allocated
	StatusStarting     = "starting"     // process/pod starting up; env install may be running
	StatusRunning      = "running"
	StatusStopping     = "stopping" // stop requested, waiting for process exit
	StatusStopped      = "stopped"
	StatusFailed       = "failed"
)

// NotebookServer represents a running (or stopped) Jupyter notebook server.
// Each server is backed by a NotebookVolume that persists independently.
type NotebookServer struct {
	ProjectID string    `json:"project_id" db:"project_id"`
	Name      string    `json:"name"       db:"name"`
	Status    string    `json:"status"     db:"status"`
	Env       string    `json:"env"        db:"env"`
	Endpoint  string    `json:"endpoint"   db:"endpoint"`
	PID       int       `json:"pid"        db:"pid"`
	WorkDir   string    `json:"work_dir"   db:"work_dir"`
	Token     string    `json:"token"      db:"token"`
	RuntimeID string    `json:"runtime_id" db:"runtime_id"`
	VolumeID  string    `json:"volume_id"  db:"volume_id"`
	Image     string    `json:"image"      db:"image"`
	YAML      string    `json:"yaml"       db:"yaml"`
	CreatedBy string    `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// NotebookServerResponse is the REST-facing (wire) representation of a
// NotebookServer. It deliberately excludes two fields the storage model
// carries:
//
//   - Token: a live Jupyter connection secret. It must never leave Piper in
//     any REST/MCP response, log line, or audit event (see a.md §3.1/§7,
//     "Jupyter 접속 토큰은 Piper 내부 비밀이다").
//   - PID: an internal OS process identifier with no product use — the
//     frontend type (frontend/src/features/notebooks/types.ts) declares it
//     but never renders it, and it was never part of the documented
//     `NotebookServer` OpenAPI schema (docs/openapi.yaml) to begin with.
//
// Endpoint and WorkDir are kept: the existing Notebook detail UI
// (frontend/src/features/notebooks/components/NotebookDetailPanel.tsx) and
// the notebook list columns display them today for the project
// member/admin managing that notebook server, and both are already part of
// the documented OpenAPI schema. This is a REST-only decision — a future
// MCP public model (a.md §4.2/§8.3) is a separate, stricter DTO for
// external AI clients and must not reuse this type.
//
// Every REST handler that returns a NotebookServer (or a slice of them)
// MUST map through NewNotebookServerResponse / NewNotebookServerResponses
// rather than serializing *NotebookServer directly — see pkg/notebook/handler.go.
type NotebookServerResponse struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Env       string    `json:"env"`
	Endpoint  string    `json:"endpoint"`
	WorkDir   string    `json:"work_dir"`
	RuntimeID string    `json:"runtime_id"`
	VolumeID  string    `json:"volume_id"`
	Image     string    `json:"image"`
	YAML      string    `json:"yaml"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewNotebookServerResponse maps a storage NotebookServer to its public
// wire representation. Returns nil for a nil input so handlers can pass
// through repository lookups (which may return nil, nil for "not found")
// without an extra nil check.
func NewNotebookServerResponse(nb *NotebookServer) *NotebookServerResponse {
	if nb == nil {
		return nil
	}
	return &NotebookServerResponse{
		ProjectID: nb.ProjectID,
		Name:      nb.Name,
		Status:    nb.Status,
		Env:       nb.Env,
		Endpoint:  nb.Endpoint,
		WorkDir:   nb.WorkDir,
		RuntimeID: nb.RuntimeID,
		VolumeID:  nb.VolumeID,
		Image:     nb.Image,
		YAML:      nb.YAML,
		CreatedBy: nb.CreatedBy,
		CreatedAt: nb.CreatedAt,
		UpdatedAt: nb.UpdatedAt,
	}
}

// NewNotebookServerResponses maps a slice of storage NotebookServer records
// to their public wire representation, preserving order. Returns an empty
// (non-nil) slice for an empty/nil input so list endpoints keep returning
// `[]` rather than `null`.
func NewNotebookServerResponses(nbs []*NotebookServer) []*NotebookServerResponse {
	out := make([]*NotebookServerResponse, 0, len(nbs))
	for _, nb := range nbs {
		out = append(out, NewNotebookServerResponse(nb))
	}
	return out
}

// NotebookHistory is an immutable record of a past notebook lifecycle
// (a prior run before a restart, or the final state before delete). Token is
// deliberately omitted — it is a live connection secret, not history.
type NotebookHistory struct {
	ID         int       `json:"id"          db:"id"`
	ProjectID  string    `json:"project_id"  db:"project_id"`
	Name       string    `json:"name"        db:"name"`
	Status     string    `json:"status"      db:"status"`
	Env        string    `json:"env"         db:"env"`
	Endpoint   string    `json:"endpoint"    db:"endpoint"`
	PID        int       `json:"pid"         db:"pid"`
	WorkDir    string    `json:"work_dir"    db:"work_dir"`
	RuntimeID  string    `json:"runtime_id"  db:"runtime_id"`
	VolumeID   string    `json:"volume_id"   db:"volume_id"`
	Image      string    `json:"image"       db:"image"`
	YAML       string    `json:"yaml"        db:"yaml"`
	CreatedBy  string    `json:"created_by,omitempty" db:"created_by"`
	DeployedAt time.Time `json:"deployed_at" db:"deployed_at"`
	StoppedAt  time.Time `json:"stopped_at"  db:"stopped_at"`
}
