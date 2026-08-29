// Package mcp implements docs/jupyter-mcp-execution.md Phase 2: a
// project-scoped MCP (Model Context Protocol) Streamable HTTP endpoint
// exposing read-only tools and resources over pkg/notebook/execution's
// domain service and pkg/notebook's server list — nothing beyond what
// design doc §16's Phase 2 checklist calls for (no kernel/execute/cancel
// tools; those are Phase 3).
//
// Package boundary (design doc §4.1, mirrored from
// pkg/notebook/execution's own boundary rule): this package calls into
// execution.Service and notebook.Repository — the same dependency
// direction pkg/notebook/execution.Handler (REST) already uses — and never
// touches execution.Repository or NotebookGateway directly. The
// protocol-only JSON-RPC/transport machinery lives in the sibling
// github.com/loykin/piper/pkg/mcp package, which knows nothing about
// notebooks; this package is the Piper-domain wiring on top of it.
package mcp

import (
	"time"

	"github.com/loykin/piper/pkg/notebook"
)

// NotebookServerPublic is the MCP-facing public representation of a
// notebook.NotebookServer. It is a strictly narrower DTO than REST's own
// notebook.NotebookServerResponse (pkg/notebook/model.go): design doc §3.1
// requires Token never leave Piper in any response, and — unlike the REST
// DTO, which is used by the notebook management UI a project member/admin
// is already looking at — an external MCP/AI client additionally gets no
// Endpoint, WorkDir, or PID either (design doc §4.2/§8.3: "Endpoint,
// WorkDir, PID도 외부 AI에 기본 반환할 필요가 없으므로 MCP 결과는 REST 저장 모델을
// 그대로 직렬화하지 않고 별도 공개 모델을 사용한다"). Reusing
// notebook.NotebookServerResponse here would be wrong even though it also
// excludes Token: it still carries Endpoint and WorkDir.
type NotebookServerPublic struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Env       string    `json:"env"`
	RuntimeID string    `json:"runtime_id"`
	VolumeID  string    `json:"volume_id"`
	Image     string    `json:"image"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewNotebookServerPublic maps a notebook.NotebookServer to its MCP public
// representation. Returns nil for nil input.
func NewNotebookServerPublic(nb *notebook.NotebookServer) *NotebookServerPublic {
	if nb == nil {
		return nil
	}
	return &NotebookServerPublic{
		ProjectID: nb.ProjectID,
		Name:      nb.Name,
		Status:    nb.Status,
		Env:       nb.Env,
		RuntimeID: nb.RuntimeID,
		VolumeID:  nb.VolumeID,
		Image:     nb.Image,
		CreatedBy: nb.CreatedBy,
		CreatedAt: nb.CreatedAt,
		UpdatedAt: nb.UpdatedAt,
	}
}

// NewNotebookServerPublics maps a slice, preserving order and returning a
// non-nil empty slice for empty/nil input.
func NewNotebookServerPublics(nbs []*notebook.NotebookServer) []*NotebookServerPublic {
	out := make([]*NotebookServerPublic, 0, len(nbs))
	for _, nb := range nbs {
		out = append(out, NewNotebookServerPublic(nb))
	}
	return out
}

// ContentEntryPublic is the MCP wire shape for execution.ContentEntry
// (design doc §8.2's piper_list_notebook_files: "경로/크기/수정 시각").
type ContentEntryPublic struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}
