package federation

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/security"
)

type Handler struct {
	repo       Repository
	homeID     string
	authorizer security.Authorizer
}

func NewHandler(repo Repository, homeID string, authorizer security.Authorizer) *Handler {
	return &Handler{repo: repo, homeID: homeID, authorizer: authorizer}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	admin := rg.Group("/federation", h.requireSystemAdmin())
	admin.GET("/members", h.listMembers)
	admin.GET("/audit-events", h.listAuditEvents)
}

func (h *Handler) requireSystemAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.authorizer == nil {
			c.Next()
			return
		}
		identity, _ := security.IdentityFromContext(c.Request.Context())
		if err := h.authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "system admin required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (h *Handler) listMembers(c *gin.Context) {
	members, err := h.repo.ListMembers(c.Request.Context(), h.homeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, members)
}

func (h *Handler) listAuditEvents(c *gin.Context) {
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 500"})
			return
		}
		limit = parsed
	}
	events, err := h.repo.ListAuditEvents(c.Request.Context(), h.homeID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, events)
}
