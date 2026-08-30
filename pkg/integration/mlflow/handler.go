package mlflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/integration/outbox"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// HandlerDeps wires the REST API (design doc section 11.1/11.2) to its
// collaborators. Only the Integrations CRUD/test endpoints and the run-link
// read endpoint are implemented in this phase — MLflowSyncJob (section
// 11.3) is out of scope, same as the reconciler it exists to drive.
type HandlerDeps struct {
	Repo        Repository
	Outbox      outbox.Repository
	Clients     ClientFactory
	Credentials interface {
		ValidateMlflowCredential(ctx context.Context, projectID, name string) error
	}
	DispatcherEnabled bool
	// GenID generates a new integration ID. Defaults to uuid.NewString.
	GenID func() string
}

type Handler struct {
	deps HandlerDeps
}

var errInvalidCredentialRef = errors.New("invalid credential reference")

func NewHandler(deps HandlerDeps) *Handler {
	if deps.GenID == nil {
		deps.GenID = uuid.NewString
	}
	return &Handler{deps: deps}
}

func (h *Handler) validateCredentialRef(ctx context.Context, projectID, ref string) error {
	if h.deps.Credentials == nil {
		return errors.New("credential store is unavailable")
	}
	err := h.deps.Credentials.ValidateMlflowCredential(ctx, projectID, strings.TrimSpace(ref))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, credential.ErrNotFound), errors.Is(err, credential.ErrDisabled), errors.Is(err, credential.ErrInvalid):
		return fmt.Errorf("%w: %s", errInvalidCredentialRef, err.Error())
	default:
		return err
	}
}

func writeCredentialRefError(c *gin.Context, err error) {
	if errors.Is(err, errInvalidCredentialRef) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slog.Error("mlflow: credential validation failed", "err", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate MLflow credential"})
}

// RegisterRoutes mounts the Integrations CRUD/test endpoints and the
// run-link read endpoint on rg (a project-scoped router group — see
// member_project.go's registerMemberProjectRoutes for the composition
// pattern every other project-scoped domain handler in this codebase
// follows). Read endpoints require only viewer; write endpoints require
// admin (design doc section 11.1's role column).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/mlflow-integrations", h.list)
	rg.GET("/mlflow-integrations/:id", h.get)
	rg.GET("/runs/:id/mlflow-links", h.runLinks)

	admin := rg.Group("", project.RequireRole(security.ProjectRoleAdmin))
	admin.POST("/mlflow-integrations", h.create)
	admin.PUT("/mlflow-integrations/:id", h.update)
	admin.DELETE("/mlflow-integrations/:id", h.delete)
	admin.POST("/mlflow-integrations/:id/test", h.test)
}

// integrationRequest is the shared create/update request body — every
// field MLflowIntegration exposes except identity/audit fields (ID,
// CreatedBy, CreatedAt, UpdatedAt, DeletedAt), which the server owns.
// CredentialRef only ever carries a *name* (pkg/credential.Store resolves
// the actual secret separately), so this type — and MLflowIntegration
// itself — never has a credential value to leak in a response (design doc
// section 11.1: "credential을 제외한 연결 목록").
type integrationRequest struct {
	Name                     string `json:"name"`
	TrackingURI              string `json:"tracking_uri"`
	CredentialRef            string `json:"credential_ref"`
	Enabled                  bool   `json:"enabled"`
	Default                  bool   `json:"default"`
	ExportPipelines          bool   `json:"export_pipelines"`
	ExportNotebookExecutions bool   `json:"export_notebook_executions"`
	ExperimentTemplate       string `json:"experiment_template"`
	ArtifactMode             string `json:"artifact_mode"`
}

func (h *Handler) list(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	limit, offset := httpx.ParseLimitOffset(c)
	items, err := h.deps.Repo.ListIntegrations(c.Request.Context(), projectContext.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if limit > 0 {
		total, err := h.deps.Repo.CountIntegrations(c.Request.Context(), projectContext.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		httpx.SetTotalCountHeader(c, limit, total)
	}
	backlogs := map[string]outbox.Backlog{}
	if h.deps.Outbox != nil && len(items) > 0 {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.ID
		}
		if b, err := h.deps.Outbox.Backlog(c.Request.Context(), ids); err == nil {
			backlogs = b
		}
	}
	details := make([]integrationDetail, 0, len(items))
	for _, item := range items {
		details = append(details, h.integrationDetailFromBacklog(item, backlogs[item.ID]))
	}
	c.JSON(http.StatusOK, details)
}

// integrationDetail is the GET-by-id response: the integration plus a small
// health/backlog snapshot (design doc section 11.1: "연결/health/backlog").
type integrationDetail struct {
	*MLflowIntegration
	SystemEnabled       bool    `json:"system_enabled"`
	Health              string  `json:"health"` // healthy | degraded | disabled
	PendingEvents       int     `json:"pending_events"`
	DeadEvents          int     `json:"dead_events"`
	OldestPendingAgeSec float64 `json:"oldest_pending_age_seconds,omitempty"`
}

// integrationDetail builds the single-item detail view, fetching item's own
// backlog in one round trip. list() instead fetches every row's backlog in
// one batched call (Outbox.Backlog) and calls integrationDetailFromBacklog
// directly, since looping this method per row would reintroduce the same
// per-row query fan-out Backlog exists to avoid.
func (h *Handler) integrationDetail(ctx context.Context, item *MLflowIntegration) integrationDetail {
	backlog := outbox.Backlog{}
	if h.deps.Outbox != nil {
		if b, err := h.deps.Outbox.Backlog(ctx, []string{item.ID}); err == nil {
			backlog = b[item.ID]
		}
	}
	return h.integrationDetailFromBacklog(item, backlog)
}

func (h *Handler) integrationDetailFromBacklog(item *MLflowIntegration, backlog outbox.Backlog) integrationDetail {
	detail := integrationDetail{
		MLflowIntegration: item,
		SystemEnabled:     h.deps.DispatcherEnabled,
		Health:            "disabled",
		PendingEvents:     backlog.Pending,
		DeadEvents:        backlog.Dead,
	}
	if backlog.OldestPending != nil {
		detail.OldestPendingAgeSec = time.Since(*backlog.OldestPending).Seconds()
	}
	if h.deps.DispatcherEnabled && item.Enabled {
		detail.Health = "healthy"
		if detail.DeadEvents > 0 {
			detail.Health = "degraded"
		}
	}
	return detail
}

func (h *Handler) get(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	item, err := h.deps.Repo.GetIntegration(c.Request.Context(), projectContext.ID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if item == nil || item.IsDeleted() {
		c.JSON(http.StatusNotFound, gin.H{"error": "mlflow integration not found"})
		return
	}
	detail := h.integrationDetail(c.Request.Context(), item)
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) create(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	var req integrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.validateCredentialRef(c.Request.Context(), projectContext.ID, req.CredentialRef); err != nil {
		writeCredentialRefError(c, err)
		return
	}
	m := &MLflowIntegration{
		ID:                       h.deps.GenID(),
		ProjectID:                projectContext.ID,
		Name:                     strings.TrimSpace(req.Name),
		TrackingURI:              req.TrackingURI,
		CredentialRef:            strings.TrimSpace(req.CredentialRef),
		Enabled:                  req.Enabled,
		Default:                  req.Default,
		ExportPipelines:          req.ExportPipelines,
		ExportNotebookExecutions: req.ExportNotebookExecutions,
		ExperimentTemplate:       req.ExperimentTemplate,
		ArtifactMode:             req.ArtifactMode,
	}
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok {
		m.CreatedBy = identity.ID
	}
	if m.ArtifactMode == "" {
		m.ArtifactMode = string(ArtifactModeReference)
	}
	if err := h.deps.Repo.CreateIntegration(c.Request.Context(), m); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, m)
}

func (h *Handler) update(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	id := c.Param("id")
	existing, err := h.deps.Repo.GetIntegration(c.Request.Context(), projectContext.ID, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if existing == nil || existing.IsDeleted() {
		c.JSON(http.StatusNotFound, gin.H{"error": "mlflow integration not found"})
		return
	}
	var req integrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.validateCredentialRef(c.Request.Context(), projectContext.ID, req.CredentialRef); err != nil {
		writeCredentialRefError(c, err)
		return
	}
	updated := &MLflowIntegration{
		ID:                       id,
		ProjectID:                projectContext.ID,
		Name:                     strings.TrimSpace(req.Name),
		TrackingURI:              req.TrackingURI,
		CredentialRef:            strings.TrimSpace(req.CredentialRef),
		Enabled:                  req.Enabled,
		Default:                  req.Default,
		ExportPipelines:          req.ExportPipelines,
		ExportNotebookExecutions: req.ExportNotebookExecutions,
		ExperimentTemplate:       req.ExperimentTemplate,
		ArtifactMode:             req.ArtifactMode,
		CreatedBy:                existing.CreatedBy,
	}
	if updated.ArtifactMode == "" {
		updated.ArtifactMode = string(ArtifactModeReference)
	}
	if err := h.deps.Repo.UpdateIntegration(c.Request.Context(), updated); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// delete implements design doc section 11.1's deletion semantics exactly:
// stop the dispatcher (by soft-deleting the integration — Exporter.Handle
// treats a missing/disabled/deleted integration as "park, don't process"),
// preserve pending outbox events as `disabled` rather than losing them, and
// never touch the remote MLflow experiment/run or the mapping tables.
func (h *Handler) delete(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	id := c.Param("id")
	if err := h.deps.Repo.DeleteIntegration(c.Request.Context(), projectContext.ID, id); err != nil {
		respondError(c, err)
		return
	}
	if h.deps.Outbox != nil {
		if _, err := h.deps.Outbox.DisableIntegrationEvents(c.Request.Context(), id); err != nil {
			// The integration is already soft-deleted at this point — a
			// failure here just means some pending events stay `pending`
			// instead of `disabled`, which the Exporter's own
			// integration-deleted check (Handle, exporter.go) still parks
			// safely on its own. Not worth failing the whole delete over.
			slog.Warn("mlflow: failed disabling outbox events for deleted integration", "integration_id", id, "err", err)
		}
	}
	c.Status(http.StatusNoContent)
}

type testResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// test implements design doc section 5.3's connection test: a single
// bounded experiment lookup, never a mutating create, with only a
// redacted success/failure message persisted or returned — never a raw
// remote response body or credential.
func (h *Handler) test(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	integration, err := h.deps.Repo.GetIntegration(c.Request.Context(), projectContext.ID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if integration == nil || integration.IsDeleted() {
		c.JSON(http.StatusNotFound, gin.H{"error": "mlflow integration not found"})
		return
	}
	client, err := h.deps.Clients(c.Request.Context(), integration)
	if err != nil {
		c.JSON(http.StatusOK, testResult{OK: false, Message: redactErr(err)})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	// A GetExperimentByName probe never creates or mutates anything —
	// whether the probe name resolves to a real experiment or not, a clean
	// (possibly not-found) response proves connectivity + auth work.
	if _, err := client.GetExperimentByName(ctx, "__piper_connection_test__"); err != nil {
		c.JSON(http.StatusOK, testResult{OK: false, Message: redactErr(err)})
		return
	}
	c.JSON(http.StatusOK, testResult{OK: true, Message: "connected"})
}

// runLinkView is the safe, credential-free projection of MLflowRunLink
// returned by GET /runs/{id}/mlflow-links (design doc section 11.2:
// "MLflow API credential이나 raw artifact URI는 반환하지 않는다" — this type
// already has neither).
type runLinkView struct {
	IntegrationID    string     `json:"integration_id"`
	MLflowRunID      string     `json:"mlflow_run_id,omitempty"`
	MLflowRunURL     string     `json:"mlflow_run_url,omitempty"`
	SyncStatus       string     `json:"sync_status"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorMessage string     `json:"last_error_message,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
}

// runLinks returns every MLflowRunLink for run :id — in practice at most
// one, since v1 allows only a single Default=true integration per project
// (design doc section 5.1), but this returns a list for forward
// compatibility with a future multi-integration fan-out.
func (h *Handler) runLinks(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	runID := c.Param("id")
	integrations, err := h.deps.Repo.ListIntegrations(c.Request.Context(), projectContext.ID, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	views := make([]runLinkView, 0, len(integrations))
	for _, integration := range integrations {
		link, err := h.deps.Repo.GetRunLink(c.Request.Context(), integration.ID, projectContext.ID, string(SourceTypePipeline), runID)
		if err != nil || link == nil {
			continue
		}
		views = append(views, runLinkView{
			IntegrationID:    link.IntegrationID,
			MLflowRunID:      link.MLflowRunID,
			MLflowRunURL:     link.MLflowRunURL,
			SyncStatus:       link.SyncStatus,
			LastErrorCode:    link.LastErrorCode,
			LastErrorMessage: link.LastErrorMessage,
			LastSyncedAt:     link.LastSyncedAt,
		})
	}
	c.JSON(http.StatusOK, views)
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
