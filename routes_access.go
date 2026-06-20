package piper

import (
	"net/http"

	"github.com/gin-gonic/gin"

	iagent "github.com/piper/piper/internal/agent"
	authpkg "github.com/piper/piper/pkg/auth"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
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
		authpkg.NewUserHandler(directory, p.cfg.Auth.UserManager).
			RegisterRoutes(userAPI.Group("", p.requireSystemAdmin()))
	}
	if members := p.cfg.Auth.ProjectMemberManager; members != nil {
		project.NewMemberHandler(members).RegisterRoutes(userAPI.Group(
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
	admin.GET("/storage/settings", func(c *gin.Context) {
		view, err := p.StorageSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	admin.PUT("/storage/settings", func(c *gin.Context) {
		var cfg StorageConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		view, err := p.UpdateStorageSettings(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	})
	return admin
}

func (p *Piper) registerWorkerRoutes(admin *gin.RouterGroup) {
	iagent.NewHandler(p.agentRegistry).RegisterRoutes(admin)
}
