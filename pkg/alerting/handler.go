package alerting

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/alert-rules", h.list)
	rg.GET("/alert-rules/:id", h.get)
	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/alert-rules", h.create)
	member.PATCH("/alert-rules/:id", h.patch)
	member.DELETE("/alert-rules/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	p, _ := project.FromContext(c.Request.Context())
	limit, offset := httpx.ParseLimitOffset(c)
	items, err := h.service.List(c.Request.Context(), p.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if limit > 0 {
		total, err := h.service.Count(c.Request.Context(), p.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		httpx.SetTotalCountHeader(c, limit, total)
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) get(c *gin.Context) {
	p, _ := project.FromContext(c.Request.Context())
	rule, err := h.service.Get(c.Request.Context(), p.ID, c.Param("id"))
	if err != nil {
		h.respond(c, err)
		return
	}
	if rule == nil {
		h.respond(c, ErrNotFound)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (h *Handler) create(c *gin.Context) {
	p, _ := project.FromContext(c.Request.Context())
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	actor := ""
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok {
		actor = identity.ID
	}
	rule, err := h.service.Create(c.Request.Context(), p.ID, actor, req)
	if err != nil {
		h.respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, rule)
}

func (h *Handler) patch(c *gin.Context) {
	p, _ := project.FromContext(c.Request.Context())
	var req PatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule, err := h.service.Patch(c.Request.Context(), p.ID, c.Param("id"), req)
	if err != nil {
		h.respond(c, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (h *Handler) delete(c *gin.Context) {
	p, _ := project.FromContext(c.Request.Context())
	if err := h.service.Delete(c.Request.Context(), p.ID, c.Param("id")); err != nil {
		h.respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) respond(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
