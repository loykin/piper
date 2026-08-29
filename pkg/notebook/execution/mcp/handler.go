package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	piperMCP "github.com/loykin/piper/pkg/mcp"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// ServerName/ServerVersion identify Piper in the MCP "initialize" handshake.
const (
	ServerName    = "piper"
	ServerVersion = "phase2"
)

// Deps are Handler's constructor dependencies — the same two capabilities
// pkg/notebook.Handler and pkg/notebook/execution.Handler each already
// expose separately; this package depends on both directly rather than
// inventing a third service layer, the same dependency direction the
// existing REST handlers use (design doc §4.1).
type Deps struct {
	// Notebooks lists/gets notebook servers for piper_list_notebook_servers
	// / piper_get_notebook_server — pkg/notebook.Handler's own
	// listNotebooks/getNotebook read this same repository directly, so
	// doing the same here isn't a boundary violation: there is no separate
	// "notebook server service" layer in this codebase to go through
	// instead.
	Notebooks notebook.Repository
	// Executions is the Phase 1 domain service every other Phase 2 tool and
	// resource goes through — never bypassed in favor of
	// execution.Repository or execution.NotebookGateway directly.
	Executions *execution.Service
}

// Config configures the MCP transport surface (design doc §8.1, §15).
type Config struct {
	// AllowedOrigins is passed straight through to
	// pkg/mcp.OriginHostPolicy.AllowedOrigins.
	AllowedOrigins []string
	// AllowedHosts is passed straight through to
	// pkg/mcp.OriginHostPolicy.AllowedHosts.
	AllowedHosts []string
	// SessionTTL is passed straight through to pkg/mcp.NewSessionStore.
	SessionTTL time.Duration
}

// Handler is the Gin handler for design doc §8's
// POST /api/projects/{project_id}/mcp Streamable HTTP endpoint.
type Handler struct {
	deps     Deps
	sessions *piperMCP.SessionStore
	origin   piperMCP.OriginHostPolicy
}

// NewHandler constructs a Handler.
func NewHandler(deps Deps, cfg Config) *Handler {
	return &Handler{
		deps:     deps,
		sessions: piperMCP.NewSessionStore(cfg.SessionTTL),
		origin:   piperMCP.OriginHostPolicy{AllowedHosts: cfg.AllowedHosts, AllowedOrigins: cfg.AllowedOrigins},
	}
}

// RegisterRoutes mounts the MCP endpoint on rg — a project-scoped router
// group that already carries :project_id and at least viewer role (see
// pkg/notebook/execution.Handler.RegisterRoutes's doc comment for the
// sibling convention this mirrors; member_project.go registers this the
// same way, gated by its own enabled flag).
//
// Only POST is implemented (design doc's own framing: "GET/SSE는 v1에서
// 제공하지 않는다" is explicitly optional, and POST-only JSON-RPC responses
// relay through the existing buffered memberProjectAPI/relayRemoteProject
// path far more simply than a long-lived SSE stream would).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/mcp", h.serveMCP)
}

func actorFrom(c *gin.Context, clientID string) execution.Actor {
	pctx, _ := project.FromContext(c.Request.Context())
	actor := execution.Actor{Role: pctx.Role, ClientID: clientID, ID: identityIDFrom(c)}
	return actor
}

// identityIDFrom returns the current request's resolved identity id, or ""
// when unauthenticated/trusted-mode has no identity — matching how
// actorFrom already treated a missing identity before this was split out.
func identityIDFrom(c *gin.Context) string {
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
		return identity.ID
	}
	return ""
}

// peekedInitialize is a tolerant, partial decode of a single JSON-RPC
// request object used only to answer two questions before the real
// pkg/mcp.Server dispatch runs: is this the "initialize" call (which gets
// the MCP-Protocol-Version-header exemption and mints a new session), and
// if so, what MCP client id should the new session bind to (design doc
// §8.1: session bound to "MCP client ID (initialize 요청의 clientInfo)").
type peekedInitialize struct {
	Method string `json:"method"`
	Params struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	} `json:"params"`
}

// peekInitialize reports whether body is a single (non-batch) JSON-RPC
// request and, if so, decodes it loosely. A batch request (starts with
// '[') is never treated as an initialize call — real MCP clients always
// send "initialize" alone, and design doc §8.1's session bootstrap only
// needs to special-case that one well-known shape.
func peekInitialize(body []byte) (peekedInitialize, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return peekedInitialize{}, false
	}
	var p peekedInitialize
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return peekedInitialize{}, false
	}
	return p, true
}

// serveMCP implements the Streamable HTTP POST endpoint: role floor,
// Origin/Host validation, MCP-Protocol-Version negotiation, session
// issuance/validation, then JSON-RPC dispatch.
func (h *Handler) serveMCP(c *gin.Context) {
	pctx, ok := project.FromContext(c.Request.Context())
	if !ok || pctx.Role < security.ProjectRoleViewer {
		// Defensive: every real mount point already floors at viewer
		// (project.Require(...ProjectRoleViewer) in serve.go, the trusted
		// Member tunnel in newMemberProjectRouter), but this handler
		// doesn't rely on that alone — see design doc §9.1's RBAC table
		// and the task's explicit "RBAC test" requirement.
		security.RespondForbidden(c, "insufficient project role")
		return
	}

	if err := h.origin.Validate(c.Request); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read request body failed"})
		return
	}

	peeked, isSingle := peekInitialize(body)
	isInitialize := isSingle && peeked.Method == "initialize"

	if err := piperMCP.ValidateProtocolVersionHeader(c.GetHeader("MCP-Protocol-Version"), isInitialize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var clientID string
	if isInitialize {
		clientID = strings.TrimSpace(peeked.Params.ClientInfo.Name)
		if clientID == "" {
			clientID = "mcp-unknown-client"
		}
	} else {
		sessionID := c.GetHeader(piperMCP.SessionIDHeader)
		if sessionID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing " + piperMCP.SessionIDHeader + " header"})
			return
		}
		sess, ok := h.sessions.Get(sessionID)
		if !ok || sess.ProjectID != pctx.ID || sess.IdentityID != identityIDFrom(c) {
			// design doc §8.1: an invalid/expired session must not silently
			// fall back to an unauthenticated call — the client must
			// re-initialize. The identity check additionally enforces the
			// doc's "사용자 ... 에 바인딩" requirement: a session ID minted
			// for one identity must not be usable by a different
			// authenticated caller even when both hold viewer+ on the same
			// project (e.g. a leaked session id, or two browser tabs
			// signed in as different users against the same reverse
			// proxy) — otherwise IdentityID would be recorded on Create
			// but never actually mean anything.
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown or expired MCP session"})
			return
		}
		h.sessions.Touch(sessionID)
		clientID = sess.ClientID
	}

	actor := actorFrom(c, clientID)
	ctx := withRequestScope(c.Request.Context(), actor, pctx.ID)

	server := h.buildServer(pctx.ID)
	out, hasResponse := server.HandleMessage(ctx, body)

	if isInitialize && hasResponse {
		sess, err := h.sessions.Create(actor.ID, pctx.ID, clientID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create MCP session"})
			return
		}
		c.Header(piperMCP.SessionIDHeader, sess.ID)
	}

	if !hasResponse {
		// Pure notification (or all-notification batch): design doc /
		// Streamable HTTP spec both call for 202 Accepted with no body.
		c.Status(http.StatusAccepted)
		return
	}
	c.Data(http.StatusOK, "application/json", out)
}

func (h *Handler) buildServer(projectID string) *piperMCP.Server {
	d := toolDeps{Notebooks: h.deps.Notebooks, Executions: h.deps.Executions}
	return &piperMCP.Server{
		Info:              piperMCP.ServerInfo{Name: ServerName, Version: ServerVersion},
		Instructions:      "Read-only access to Piper-managed Jupyter notebooks and their execution history for project " + projectID + ".",
		Tools:             d.tools(),
		ResourceTemplates: resourceTemplates(),
		ReadResource:      d.readResource,
	}
}

// projectIDFrom/actorFromCtx are small accessors tools.go/resources.go use
// instead of importing gin — keeps those two files transport-agnostic, only
// this file and context.go know about *gin.Context / requestScope's plumbing.
func projectIDFrom(ctx context.Context) string { return requestScopeFrom(ctx).ProjectID }
