package execution

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// Handler is the Gin REST handler for design doc §7's Contents, Kernel
// sessions, and Executions API. It only calls into Service — never touches
// a Repository or NotebookGateway directly (design doc §4.1's dependency
// direction).
type Handler struct {
	svc *Service
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func currentProjectID(c *gin.Context) string {
	pctx, _ := project.FromContext(c.Request.Context())
	return pctx.ID
}

// actorFrom builds the Actor Service receives from the identity/role
// Home's own middleware already resolved for this request — the same
// pattern pkg/pipeline/run/handler.go's authFrom uses. ClientID is fixed to
// "rest" in this phase; a later MCP phase will thread through the real MCP
// client identifier instead.
func actorFrom(c *gin.Context) Actor {
	pctx, _ := project.FromContext(c.Request.Context())
	actor := Actor{Role: pctx.Role, ClientID: "rest"}
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
		actor.ID = identity.ID
	}
	return actor
}

// RegisterRoutes mounts design doc §7's routes onto rg (a project-scoped
// router group already carrying :project_id and at least viewer role —
// see pkg/notebook/handler.go's RegisterRoutes for the sibling convention
// this mirrors). Also mounts a project-level notebook-execution-policy pair
// of routes (GET viewer, PUT admin) — the simplest config surface for
// design doc §9.3's notebook_execution.mcp_policy override; see the design
// doc §9.3 comment on Service.SetPolicy for why this isn't in the doc's own
// §7 API table.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	// Contents (§7.1) and execution history/status (§7.3) are readable at
	// viewer. Kernel sessions (§7.2) are member-and-up for every verb,
	// including GET/list — unlike executions, the design doc's RBAC table
	// does not grant viewer any kernel-session visibility.
	rg.GET("/notebooks/:name/contents", h.listContents)
	rg.GET("/notebooks/:name/documents", h.getDocument)
	rg.GET("/notebooks/:name/executions", h.listExecutions)
	rg.GET("/notebooks/:name/executions/:id", h.getExecution)
	rg.GET("/notebook-executions", h.listProjectExecutions)
	rg.GET("/notebook-execution-policy", h.getPolicy)

	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.PUT("/notebooks/:name/documents", h.putDocument)
	member.GET("/notebooks/:name/kernel-sessions", h.listKernelSessions)
	member.GET("/notebooks/:name/kernel-sessions/:id", h.getKernelSession)
	member.POST("/notebooks/:name/kernel-sessions", h.createKernelSession)
	member.POST("/notebooks/:name/kernel-sessions/:id/interrupt", h.interruptKernelSession)
	member.POST("/notebooks/:name/kernel-sessions/:id/restart", h.restartKernelSession)
	member.DELETE("/notebooks/:name/kernel-sessions/:id", h.closeKernelSession)
	member.POST("/notebooks/:name/executions", h.createExecution)
	// Cancel's minimum route role is member — design doc §9.1 grants
	// "자신의 실행 취소" to member and reserves cancelling *another actor's*
	// execution for admin; that ownership distinction is enforced inside
	// Service.CancelExecution (checkOwnership), not by the route's role
	// floor, the same split pattern kernel-session interrupt/restart/close
	// already uses above.
	member.POST("/notebooks/:name/executions/:id/cancel", h.cancelExecution)

	admin := rg.Group("", project.RequireRole(security.ProjectRoleAdmin))
	admin.POST("/notebooks/:name/executions/:id/approve", h.approveExecution)
	admin.POST("/notebooks/:name/executions/:id/deny", h.denyExecution)
	admin.PUT("/notebook-execution-policy", h.setPolicy)
}

// --- error envelope --------------------------------------------------

// writeExecutionError maps a Service error to the
// {"error":...,"code":...,"retryable":...} envelope convention
// pkg/pipeline/run/handler.go's writeMemberError and
// internal/memberclient/errors.go use elsewhere in this codebase.
func writeExecutionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrLimitExceeded):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "code": ErrCodeRuntimeUnavailable, "retryable": true})
	case errors.Is(err, ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "idempotency key reused with a different request payload", "code": "idempotency_conflict", "retryable": false})
	default:
		var de *Error
		if errors.As(err, &de) {
			c.JSON(statusForCode(de.Code), gin.H{"error": de.Message, "code": de.Code, "retryable": de.Retryable})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func statusForCode(code string) int {
	switch code {
	case ErrCodeNotebookNotRunning, ErrCodeKernelUnavailable, ErrCodeKernelDied, ErrCodeRuntimeUnavailable:
		return http.StatusServiceUnavailable
	case ErrCodeExecutionTimeout:
		return http.StatusGatewayTimeout
	case ErrCodeExecutionCancelled, ErrCodeContentConflict, ErrCodeRecoveryUncertain:
		return http.StatusConflict
	case ErrCodePathInvalid:
		return http.StatusBadRequest
	case ErrCodeOutputTooLarge:
		return http.StatusRequestEntityTooLarge
	case ErrCodeApprovalRequired, ErrCodeApprovalDenied:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// --- Contents ----------------------------------------------------------

func (h *Handler) listContents(c *gin.Context) {
	entries, err := h.svc.ListContents(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Query("path"))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, entries)
}

func (h *Handler) getDocument(c *gin.Context) {
	doc, hash, err := h.svc.ReadDocument(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Query("path"))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": doc, "content_hash": hash})
}

func (h *Handler) putDocument(c *gin.Context) {
	var body struct {
		BaseHash string          `json:"base_hash"`
		Content  json.RawMessage `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	doc, err := jupyter.ParseNotebook(body.Content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notebook content: " + err.Error()})
		return
	}
	if err := h.svc.WriteDocument(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), c.Query("path"), doc, body.BaseHash); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Kernel sessions -----------------------------------------------------

func (h *Handler) createKernelSession(c *gin.Context) {
	var body struct {
		Path       string `json:"path"`
		KernelName string `json:"kernel_name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ks, err := h.svc.CreateKernelSession(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), CreateKernelSessionRequest{
		NotebookPath: body.Path,
		KernelName:   body.KernelName,
	})
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusCreated, NewKernelSessionResponse(ks))
}

func (h *Handler) listKernelSessions(c *gin.Context) {
	limit, offset := httpx.ParseLimitOffset(c)
	list, err := h.svc.ListKernelSessions(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), limit, offset)
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, NewKernelSessionResponses(list))
}

func (h *Handler) getKernelSession(c *gin.Context) {
	ks, err := h.svc.GetKernelSessionForActor(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), c.Param("id"))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, NewKernelSessionResponse(ks))
}

func (h *Handler) interruptKernelSession(c *gin.Context) {
	if _, err := h.svc.GetKernelSessionForActor(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.InterruptKernelSession(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) restartKernelSession(c *gin.Context) {
	if _, err := h.svc.GetKernelSessionForActor(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.RestartKernelSession(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) closeKernelSession(c *gin.Context) {
	if _, err := h.svc.GetKernelSessionForActor(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.CloseKernelSession(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Executions ----------------------------------------------------------

func (h *Handler) createExecution(c *gin.Context) {
	var body struct {
		Kind            string `json:"kind"`
		Path            string `json:"path"`
		KernelSessionID string `json:"kernel_session_id"`
		Edit            *struct {
			Mode   string `json:"mode"`
			Code   string `json:"code"`
			CellID string `json:"cell_id"`
		} `json:"edit"`
		CreateIfMissing bool `json:"create_if_missing"`
		TimeoutSeconds  int  `json:"timeout_seconds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req := CreateExecutionRequest{
		Kind:            body.Kind,
		Path:            body.Path,
		KernelSessionID: body.KernelSessionID,
		CreateIfMissing: body.CreateIfMissing,
		TimeoutSeconds:  body.TimeoutSeconds,
	}
	if body.Edit != nil {
		req.Edit = &CellEdit{Mode: body.Edit.Mode, Code: body.Edit.Code, CellID: body.Edit.CellID}
	}

	exec, replayed, err := h.svc.CreateExecution(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("name"), req, c.GetHeader("Idempotency-Key"))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	c.JSON(status, NewNotebookExecutionResponse(exec))
}

func (h *Handler) listExecutions(c *gin.Context) {
	limit, offset := httpx.ParseLimitOffset(c)
	list, total, err := h.svc.ListExecutions(c.Request.Context(), currentProjectID(c), c.Param("name"), limit, offset)
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	if limit > 0 {
		httpx.SetTotalCountHeader(c, limit, total)
	}
	c.JSON(http.StatusOK, NewNotebookExecutionResponses(list))
}

func (h *Handler) listProjectExecutions(c *gin.Context) {
	limit, offset := httpx.ParseLimitOffset(c)
	list, total, err := h.svc.ListExecutions(c.Request.Context(), currentProjectID(c), strings.TrimSpace(c.Query("notebook")), limit, offset)
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	if limit > 0 {
		httpx.SetTotalCountHeader(c, limit, total)
	}
	c.JSON(http.StatusOK, NewNotebookExecutionResponses(list))
}

func (h *Handler) getExecution(c *gin.Context) {
	exec, err := h.svc.GetExecutionForNotebook(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Param("id"))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, NewNotebookExecutionResponse(exec))
}

func (h *Handler) cancelExecution(c *gin.Context) {
	if _, err := h.svc.GetExecutionForNotebook(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.CancelExecution(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) approveExecution(c *gin.Context) {
	if _, err := h.svc.GetExecutionForNotebook(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.ApproveExecution(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) denyExecution(c *gin.Context) {
	if _, err := h.svc.GetExecutionForNotebook(c.Request.Context(), currentProjectID(c), c.Param("name"), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	if err := h.svc.DenyExecution(c.Request.Context(), actorFrom(c), currentProjectID(c), c.Param("id")); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Policy ---------------------------------------------------------------

func (h *Handler) getPolicy(c *gin.Context) {
	policy, err := h.svc.GetPolicy(c.Request.Context(), currentProjectID(c))
	if err != nil {
		writeExecutionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"mcp_policy": policy})
}

func (h *Handler) setPolicy(c *gin.Context) {
	var body struct {
		MCPPolicy string `json:"mcp_policy"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.SetPolicy(c.Request.Context(), actorFrom(c), currentProjectID(c), body.MCPPolicy); err != nil {
		writeExecutionError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
