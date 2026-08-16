package project

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/internal/httpx"
	"github.com/loykin/piper/pkg/security"
)

// MemberHandler owns project membership HTTP endpoints.
type MemberHandler struct {
	members security.ProjectMemberManager
	users   security.UserDirectory
	lookup  security.UserLookup
}

func NewMemberHandler(members security.ProjectMemberManager, users ...security.UserDirectory) *MemberHandler {
	h := &MemberHandler{members: members}
	if len(users) > 0 {
		h.users = users[0]
		h.lookup, _ = users[0].(security.UserLookup)
	}
	return h
}

// RegisterRoutes mounts member management routes on a project-scoped group.
func (h *MemberHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/members", h.list)

	admin := rg.Group("", RequireRole(security.ProjectRoleAdmin))
	admin.GET("/members/candidates", h.candidates)
	admin.POST("/members", h.add)
	admin.PUT("/members/:user_id", h.update)
	admin.DELETE("/members/:user_id", h.remove)
}

type memberView struct {
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username,omitempty"`
	Role      string `json:"role"`
}

type memberCandidateView struct {
	Username string `json:"username"`
}

func (h *MemberHandler) candidates(c *gin.Context) {
	if h.users == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "user directory is unavailable"})
		return
	}
	projectContext, _ := FromContext(c.Request.Context())
	users, err := h.users.ListUsers(c.Request.Context(), 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	members, err := h.members.ListMembers(c.Request.Context(), projectContext.ID, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	existing := make(map[string]struct{}, len(members))
	for _, member := range members {
		existing[member.UserID] = struct{}{}
	}
	out := make([]memberCandidateView, 0, len(users))
	for _, user := range users {
		if user == nil || user.Disabled || user.SystemAdmin || user.Username == "" {
			continue
		}
		if _, found := existing[user.ID]; found {
			continue
		}
		out = append(out, memberCandidateView{Username: user.Username})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	c.JSON(http.StatusOK, out)
}

func (h *MemberHandler) list(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	limit, offset := httpx.ParseLimitOffset(c)
	members, err := h.members.ListMembers(c.Request.Context(), projectContext.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if limit > 0 {
		total, err := h.members.CountMembers(c.Request.Context(), projectContext.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		httpx.SetTotalCountHeader(c, limit, total)
	}
	out := make([]memberView, len(members))
	for i, member := range members {
		out[i] = h.memberView(c, member)
	}
	c.JSON(http.StatusOK, out)
}

func (h *MemberHandler) add(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	var req struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := req.UserID
	if userID == "" && req.Username != "" && h.lookup != nil {
		user, err := h.lookup.FindUser(c.Request.Context(), req.Username)
		if err != nil || user == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
			return
		}
		userID = user.ID
	}
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}
	now := time.Now().UTC()
	member := &security.ProjectMember{
		ProjectID: projectContext.ID,
		UserID:    userID,
		Role:      req.Role,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.members.AddMember(c.Request.Context(), member); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, h.memberView(c, member))
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
	c.JSON(http.StatusOK, h.memberView(c, member))
}

func (h *MemberHandler) memberView(c *gin.Context, member *security.ProjectMember) memberView {
	view := memberView{ProjectID: member.ProjectID, UserID: member.UserID, Role: member.Role}
	if h.users != nil {
		if user, err := h.users.GetUser(c.Request.Context(), member.UserID); err == nil && user != nil {
			view.Username = user.Username
		}
	}
	return view
}

func (h *MemberHandler) remove(c *gin.Context) {
	projectContext, _ := FromContext(c.Request.Context())
	if err := h.members.RemoveMember(c.Request.Context(), projectContext.ID, c.Param("user_id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
