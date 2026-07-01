package credential

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/credentials", h.list)
	rg.GET("/credentials/:name", h.get)

	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/credentials", h.create)
	member.PATCH("/credentials/:name", h.patch)
	member.POST("/credentials/:name/rotate", h.rotate)
	member.POST("/credentials/:name/test", h.test)
	member.DELETE("/credentials/:name", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	items, err := h.store.List(c.Request.Context(), projectContext.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) get(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	item, err := h.store.Get(c.Request.Context(), projectContext.ID, c.Param("name"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *Handler) create(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := h.store.Create(c.Request.Context(), projectContext.ID, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *Handler) rotate(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	var req RotateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.store.Rotate(c.Request.Context(), projectContext.ID, c.Param("name"), req); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) patch(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "credential name is required"})
		return
	}
	var req PatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := h.store.Patch(c.Request.Context(), projectContext.ID, name, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *Handler) test(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	name := strings.TrimSpace(c.Param("name"))
	var req TestRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	result, err := h.store.Test(c.Request.Context(), projectContext.ID, name, req)
	if err != nil {
		respondError(c, err)
		return
	}
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadGateway
	}
	c.JSON(status, result)
}

func (h *Handler) delete(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "credential name is required"})
		return
	}
	if err := h.store.Delete(c.Request.Context(), projectContext.ID, name); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrDisabled):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrScopeViolation):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
