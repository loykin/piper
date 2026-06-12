package project

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

var validProjectID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Handler struct {
	repo       Repository
	authorizer security.Authorizer
}

// NewHandler creates a project Handler.
// authorizer may be nil only in trusted mode.
func NewHandler(repo Repository, authorizer security.Authorizer) *Handler {
	return &Handler{repo: repo, authorizer: authorizer}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	// List: any authenticated user (filtered to their memberships in auth mode).
	rg.GET("/projects", h.list)
	// Get: any authenticated user (access verified by the caller having a valid identity).
	rg.GET("/projects/:project_id", h.get)
	// Create/Delete: system admin only.
	admin := rg.Group("/", h.requireSystemAdmin())
	admin.POST("/projects", h.create)
	admin.DELETE("/projects/:project_id", h.delete)
}

// requireSystemAdmin returns a middleware that rejects non-admin callers.
// In trusted mode all callers pass.
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

func (h *Handler) list(c *gin.Context) {
	ctx := c.Request.Context()

	// In auth mode, filter to projects the caller is a member of.
	if h.authorizer != nil {
		identity, ok := security.IdentityFromContext(ctx)
		if !ok || identity == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		// The Authorizer owns system-admin policy; do not infer it from identity fields.
		if err := h.authorizer.AuthorizeSystem(ctx, identity); err != nil {
			roles, err := h.authorizer.ListProjectRoles(ctx, identity)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "role lookup failed"})
				return
			}
			if len(roles) == 0 {
				c.JSON(http.StatusOK, []*Project{})
				return
			}
			// Fetch only accessible projects.
			var projects []*Project
			for projectID := range roles {
				p, err := h.repo.Get(ctx, projectID)
				if err != nil || p == nil {
					continue
				}
				projects = append(projects, p)
			}
			if projects == nil {
				projects = []*Project{}
			}
			c.JSON(http.StatusOK, projects)
			return
		}
	}

	projects, err := h.repo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, projects)
}

func (h *Handler) create(c *gin.Context) {
	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	if !validProjectID.MatchString(req.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must contain only lowercase letters, numbers, and hyphens"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	p := &Project{ID: req.ID, Name: req.Name, Description: strings.TrimSpace(req.Description)}
	if err := h.repo.Create(c.Request.Context(), p); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) get(c *gin.Context) {
	projectID := c.Param("project_id")

	// In auth mode, verify the caller has access to this project.
	if h.authorizer != nil {
		identity, _ := security.IdentityFromContext(c.Request.Context())
		role, err := h.authorizer.ProjectRole(c.Request.Context(), identity, projectID)
		if err != nil || role < security.ProjectRoleViewer {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
	}

	p, err := h.repo.Get(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) delete(c *gin.Context) {
	projectID := c.Param("project_id")
	p, err := h.repo.Get(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), projectID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
