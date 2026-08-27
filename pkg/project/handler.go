package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/statsstore"
)

var validProjectID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Handler struct {
	repo         Repository
	authorizer   security.Authorizer
	owner        OwnerResolver
	creator      Creator
	beforeDelete BeforeDelete
}

// OwnerResolver validates and resolves the Owner Member assigned when a Home
// creates a Project. requested is the optional owner_member_id from the API.
type OwnerResolver func(projectID, requested string) (string, error)

// Creator lets a Home persist a Project and its directory audit event in one
// transaction. Standalone handlers use Repository.Create directly.
type Creator func(ctx context.Context, value *Project, actorID string) error

// BeforeDelete performs ownership-scoped cleanup that must succeed before
// project metadata is removed, such as purging statistics on the owning Member.
type BeforeDelete func(ctx context.Context, value *Project) error

// NewHandler creates a project Handler.
// authorizer may be nil only in trusted mode.
func NewHandler(repo Repository, authorizer security.Authorizer) *Handler {
	return NewHandlerWithOwner(repo, authorizer, nil)
}

func NewHandlerWithOwner(repo Repository, authorizer security.Authorizer, owner OwnerResolver) *Handler {
	return NewHandlerWithDirectory(repo, authorizer, owner, nil)
}

func NewHandlerWithDirectory(repo Repository, authorizer security.Authorizer, owner OwnerResolver, creator Creator) *Handler {
	if owner == nil {
		owner = func(_, requested string) (string, error) {
			if requested == "" || requested == LocalMemberID {
				return LocalMemberID, nil
			}
			return "", fmt.Errorf("remote Owner Member %q is not available in standalone mode", requested)
		}
	}
	return &Handler{repo: repo, authorizer: authorizer, owner: owner, creator: creator}
}

func (h *Handler) WithBeforeDelete(beforeDelete BeforeDelete) *Handler {
	h.beforeDelete = beforeDelete
	return h
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
		if identity == nil {
			security.RespondUnauthorized(c, "")
			return
		}
		if err := h.authorizer.AuthorizeSystem(c.Request.Context(), identity); err != nil {
			security.RespondForbidden(c, "system admin required")
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
			security.RespondUnauthorized(c, "")
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
	c.JSON(http.StatusOK, visibleProjects(projects))
}

// visibleProjects drops reserved system projects from a listing.
func visibleProjects(projects []*Project) []*Project {
	out := make([]*Project, 0, len(projects))
	for _, p := range projects {
		if p == nil || Reserved(p.ID) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (h *Handler) create(c *gin.Context) {
	var req struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		OwnerMemberID string `json:"owner_member_id"`
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
	if Reserved(req.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project id is reserved"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	ownerMemberID, err := h.owner(req.ID, strings.TrimSpace(req.OwnerMemberID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := &Project{ID: req.ID, Name: req.Name, Description: strings.TrimSpace(req.Description), OwnerMemberID: ownerMemberID}
	actorID := ""
	if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
		actorID = identity.ID
	}
	create := h.repo.Create
	if h.creator != nil {
		create = func(ctx context.Context, value *Project) error { return h.creator(ctx, value, actorID) }
	}
	if err := create(c.Request.Context(), p); err != nil {
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
		if identity == nil {
			security.RespondUnauthorized(c, "")
			return
		}
		role, err := h.authorizer.ProjectRole(c.Request.Context(), identity, projectID)
		if err != nil || role < security.ProjectRoleViewer {
			security.RespondForbidden(c, "forbidden")
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
	if Reserved(projectID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project id is reserved"})
		return
	}
	if projectID == DefaultID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the default project cannot be deleted"})
		return
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
	projects, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(visibleProjects(projects)) <= 1 {
		c.JSON(http.StatusConflict, gin.H{"error": "the last project cannot be deleted"})
		return
	}
	if h.beforeDelete != nil {
		if err := h.beforeDelete(c.Request.Context(), p); err != nil {
			message := err.Error()
			if errors.Is(err, statsstore.ErrBackendUnavailable) {
				message = "statistics backend unavailable"
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": message})
			return
		}
	}
	if err := h.repo.Delete(c.Request.Context(), projectID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
