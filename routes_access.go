package piper

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "github.com/loykin/piper/pkg/auth"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

func (p *Piper) registerAuthRoutes(r *gin.Engine, userAPI *gin.RouterGroup) {
	loginMode := ""
	loginURL := ""
	if routes := p.cfg.Auth.LoginRoutes; routes != nil {
		loginMode = routes.LoginMode()
		loginURL = routes.LoginURL()
	}
	r.GET("/api/capabilities", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"authentication":            !p.cfg.Auth.Trusted,
			"login_routes":              p.cfg.Auth.LoginRoutes != nil,
			"login_mode":                loginMode,
			"login_url":                 loginURL,
			"user_directory":            p.cfg.Auth.UserDirectory != nil,
			"user_management":           p.cfg.Auth.UserManager != nil,
			"project_member_management": p.cfg.Auth.ProjectMemberManager != nil,
		})
	})
	if routes := p.cfg.Auth.LoginRoutes; routes != nil {
		routes.RegisterPublicRoutes(r.Group("/api"))
		routes.RegisterAuthenticatedRoutes(userAPI)
	}
	if directory := p.cfg.Auth.UserDirectory; directory != nil {
		memberships, _ := p.cfg.Auth.ProjectMemberManager.(security.UserMembershipDirectory)
		userHandler := authpkg.NewUserHandler(directory, p.cfg.Auth.UserManager, memberships)
		userHandler.RegisterRoutes(userAPI.Group("", p.requireSystemAdmin()))
		userHandler.RegisterBootstrapRoutes(r.Group("/api"))
	}
	if members := p.cfg.Auth.ProjectMemberManager; members != nil {
		project.NewMemberHandler(members, p.cfg.Auth.UserDirectory).RegisterRoutes(userAPI.Group(
			"/projects/:project_id",
			project.Require(p.repos.Project, p.cfg.Auth.Authorizer, security.ProjectRoleViewer),
		))
	}
}

func (p *Piper) registerAdminRoutes(userAPI *gin.RouterGroup) *gin.RouterGroup {
	admin := userAPI.Group("", p.requireSystemAdmin())
	admin.GET("/settings", func(c *gin.Context) {
		c.JSON(http.StatusOK, p.Settings())
	})
	// GET /storage/settings is a read-only diagnostic: the artifact storage
	// backend (bucket/endpoint/region/which-backend) is deploy-time-only
	// configuration, the same class of setting as runtime.type or
	// server.db.driver — see storage_admin.go's StorageSettingsView doc
	// comment for the full rationale. There is deliberately no PUT here
	// anymore; changing the backend requires editing storage.yaml directly
	// on disk and restarting the server.
	admin.GET("/storage/settings", func(c *gin.Context) {
		view, err := p.StorageSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	// POST /storage/settings/test never persists anything or touches the
	// running store — it only opens a throwaway client against the given
	// candidate config to confirm it's reachable. Unlike the removed PUT,
	// this carries none of the live-backend-swap risk, so it stays as an
	// ops/API tool for validating a storage.yaml edit before applying it
	// (not wired to any frontend control since there's no longer an
	// editable form to source a candidate config from).
	admin.POST("/storage/settings/test", func(c *gin.Context) {
		var cfg StorageConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, p.TestStorageSettings(c.Request.Context(), cfg))
	})
	return admin
}
