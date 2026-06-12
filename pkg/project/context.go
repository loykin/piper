package project

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

// Context carries the resolved project and the caller's role within it.
type Context struct {
	ID   string
	Role security.ProjectRole
}

type contextKey struct{}

func WithContext(ctx context.Context, project Context) context.Context {
	return context.WithValue(ctx, contextKey{}, project)
}

func FromContext(ctx context.Context) (Context, bool) {
	project, ok := ctx.Value(contextKey{}).(Context)
	return project, ok
}

// RequireRole returns a Gin middleware that checks the caller's role in the
// current project context (set by a prior Require call).
// Used to enforce member/admin requirements on individual routes.
func RequireRole(minRole security.ProjectRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectContext, ok := FromContext(c.Request.Context())
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "project context missing"})
			c.Abort()
			return
		}
		if projectContext.Role < minRole {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient project role"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// Require is a Gin middleware that:
//  1. Resolves :project_id from the URL and verifies the project exists.
//  2. When authorizer is non-nil, ensures the caller holds at least minRole.
//     In trusted mode every caller is treated as ProjectRoleAdmin.
//
// The resulting Context (including Role) is stored in the request context.
func Require(repo Repository, authorizer security.Authorizer, minRole security.ProjectRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := c.Param("project_id")
		p, err := repo.Get(c.Request.Context(), projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		if p == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
			c.Abort()
			return
		}

		// Default to the minimum requested role; trusted mode elevates to Admin.
		role := minRole
		if authorizer == nil {
			role = security.ProjectRoleAdmin // trusted mode: full access
		} else {
			identity, _ := security.IdentityFromContext(c.Request.Context())
			resolvedRole, err := authorizer.ProjectRole(c.Request.Context(), identity, projectID)
			if err != nil || resolvedRole < minRole {
				c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				c.Abort()
				return
			}
			role = resolvedRole
		}

		ctx := WithContext(c.Request.Context(), Context{ID: p.ID, Role: role})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// RequireTrusted resolves the project for an already-authenticated
// infrastructure caller. It performs no user membership lookup.
func RequireTrusted(repo Repository) gin.HandlerFunc {
	return Require(repo, nil, security.ProjectRoleAdmin)
}
