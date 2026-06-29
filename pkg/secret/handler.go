package secret

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

type Handler struct {
	repo  Repository
	store *Store
}

func NewHandler(repo Repository, store *Store) *Handler {
	return &Handler{repo: repo, store: store}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/secrets", h.list)
	rg.GET("/secrets/:name", h.get)

	member := rg.Group("", project.RequireRole(security.ProjectRoleMember))
	member.POST("/secrets", h.create)
	member.PATCH("/secrets/:name", h.patch)
	member.POST("/secrets/:name/rotate", h.rotate)
	member.DELETE("/secrets/:name", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	items, err := h.repo.List(c.Request.Context(), projectContext.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) get(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	item, err := h.repo.Get(c.Request.Context(), projectContext.ID, c.Param("name"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "secret not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *Handler) create(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "secret store is not configured"})
		return
	}
	projectContext, _ := project.FromContext(c.Request.Context())
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Type == "" {
		req.Type = TypeEnv
	}
	item, err := h.store.Create(c.Request.Context(), projectContext.ID, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *Handler) rotate(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "secret store is not configured"})
		return
	}
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "secret name is required"})
		return
	}
	var req PatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}
	if _, err := h.requireSecret(c, projectContext.ID, name); err != nil {
		respondError(c, err)
		return
	}
	if err := h.repo.SetEnabled(c.Request.Context(), projectContext.ID, name, *req.Enabled); err != nil {
		respondError(c, err)
		return
	}
	item, err := h.repo.Get(c.Request.Context(), projectContext.ID, name)
	if err != nil {
		respondError(c, err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "secret not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *Handler) delete(c *gin.Context) {
	projectContext, _ := project.FromContext(c.Request.Context())
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "secret name is required"})
		return
	}
	if _, err := h.requireSecret(c, projectContext.ID, name); err != nil {
		respondError(c, err)
		return
	}
	if err := h.repo.Delete(c.Request.Context(), projectContext.ID, name); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) requireSecret(c *gin.Context, projectID, name string) (*Metadata, error) {
	item, err := h.repo.Get(c.Request.Context(), projectID, name)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return item, nil
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
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
