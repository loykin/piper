package project

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

// MemberHandler owns project membership HTTP endpoints.
type MemberHandler struct {
	members security.ProjectMemberManager
}

func NewMemberHandler(members security.ProjectMemberManager) *MemberHandler {
	return &MemberHandler{members: members}
}

// RegisterRoutes mounts member management routes on a project-scoped group.
func (h *MemberHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/members", h.list)

	admin := rg.Group("", RequireRole(security.ProjectRoleAdmin))
	admin.POST("/members", h.add)
	admin.PUT("/members/:user_id", h.update)
	admin.DELETE("/members/:user_id", h.remove)
}

type memberView struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
}

func (h *MemberHandler) list(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	members, err := h.members.ListMembers(c.Request.Context(), projectContext.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]memberView, len(members))
	for i, member := range members {
		out[i] = memberView{ProjectID: member.ProjectID, UserID: member.UserID, Role: member.Role}
	}
	c.JSON(http.StatusOK, out)
}

func (h *MemberHandler) add(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	var req struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	member := &security.ProjectMember{
		ProjectID: projectContext.ID,
		UserID:    req.UserID,
		Role:      req.Role,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.members.AddMember(c.Request.Context(), member); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, memberView{ProjectID: member.ProjectID, UserID: member.UserID, Role: member.Role})
}

func (h *MemberHandler) update(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	userID := c.Param("user_id")
	var req struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	member, err := h.members.GetMember(c.Request.Context(), projectContext.ID, userID)
	if err != nil || member == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	member.Role = req.Role
	if err := h.members.UpdateMember(c.Request.Context(), member); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, memberView{ProjectID: member.ProjectID, UserID: member.UserID, Role: member.Role})
}

func (h *MemberHandler) remove(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	if err := h.members.RemoveMember(c.Request.Context(), projectContext.ID, c.Param("user_id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
