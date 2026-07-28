package auth

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

const (
	cookieAccess  = "piper_access"
	cookieRefresh = "piper_refresh"
)

// Handler exposes POST /api/auth/login, POST /api/auth/refresh,
// POST /api/auth/logout, GET /api/auth/me.
type Handler struct {
	sessions LoginProvider
	users    security.UserDirectory
	secure   bool // set to true in production (HTTPS only cookies)
}

// NewHandler creates an auth Handler.
// secure should be true when the server serves over HTTPS.
func NewHandler(login LoginProvider, users security.UserDirectory, secure bool) *Handler {
	return &Handler{sessions: login, users: users, secure: secure}
}

func (h *Handler) LoginMode() string { return "password" }

func (h *Handler) LoginURL() string { return "/api/auth/login" }

// RegisterPublicRoutes mounts routes that must work without an access token.
func (h *Handler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	rg.POST("/auth/login", h.login)
	rg.POST("/auth/refresh", h.refresh)
}

// RegisterAuthenticatedRoutes mounts routes that consume the authenticated identity.
func (h *Handler) RegisterAuthenticatedRoutes(rg *gin.RouterGroup) {
	rg.POST("/auth/logout", h.logout)
	rg.GET("/auth/me", h.me)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.sessions.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	h.setTokenCookies(c, result)
	c.JSON(http.StatusOK, gin.H{
		"user": persistedUserView(result.User),
	})
}

func (h *Handler) refresh(c *gin.Context) {
	refreshToken := refreshTokenFromRequest(c)
	if refreshToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing refresh token"})
		return
	}
	result, err := h.sessions.Refresh(c.Request.Context(), refreshToken)
	if err != nil {
		h.clearTokenCookies(c)
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	h.setTokenCookies(c, result)
	c.JSON(http.StatusOK, gin.H{
		"user": persistedUserView(result.User),
	})
}

func (h *Handler) logout(c *gin.Context) {
	// Try to revoke via refresh token (cookie or JSON body).
	refreshToken := refreshTokenFromRequest(c)
	if refreshToken != "" {
		_ = h.sessions.Logout(c.Request.Context(), refreshToken)
	} else {
		// Cookie path /api/auth/refresh means the cookie is unavailable at /api/auth/logout.
		// Fall back: revoke all sessions for the authenticated identity.
		if identity, ok := security.IdentityFromContext(c.Request.Context()); ok && identity != nil {
			_ = h.sessions.RevokeAllSessions(c.Request.Context(), identity.ID)
		}
	}
	h.clearTokenCookies(c)
	c.Status(http.StatusNoContent)
}

func (h *Handler) me(c *gin.Context) {
	identity, ok := security.IdentityFromContext(c.Request.Context())
	if !ok || identity == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	u, err := h.users.GetUser(c.Request.Context(), identity.ID)
	if err != nil || u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, securityUserView(u))
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) setTokenCookies(c *gin.Context, result *LoginResult) {
	accessTTL := int(h.sessions.AccessTTL().Seconds())
	refreshTTL := int(time.Until(result.ExpiresAt).Seconds())

	c.SetCookie(cookieAccess, result.AccessToken, accessTTL, "/", "", h.secure, true)
	// path=/api/auth ensures the refresh cookie is sent to /api/auth/refresh AND /api/auth/logout
	// but not to arbitrary API endpoints (CSRF mitigation).
	c.SetCookie(cookieRefresh, result.RefreshToken, refreshTTL, "/api/auth", "", h.secure, true)
}

func (h *Handler) clearTokenCookies(c *gin.Context) {
	c.SetCookie(cookieAccess, "", -1, "/", "", h.secure, true)
	c.SetCookie(cookieRefresh, "", -1, "/api/auth", "", h.secure, true)
}

func refreshTokenFromRequest(c *gin.Context) string {
	if cookie, err := c.Request.Cookie(cookieRefresh); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	// Also accept Bearer in Authorization for API clients.
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&req); err == nil && req.RefreshToken != "" {
		return req.RefreshToken
	}
	return ""
}

// UserHandler exposes optional system-admin user directory and management APIs.
type UserHandler struct {
	directory security.UserDirectory
	manager   security.UserManager
	// bootstrapMu serializes bootstrap() so two concurrent first-run requests
	// can't both observe zero users and both create an admin account.
	bootstrapMu sync.Mutex
}

func NewUserHandler(directory security.UserDirectory, manager security.UserManager) *UserHandler {
	return &UserHandler{directory: directory, manager: manager}
}

// RegisterRoutes mounts the routes supported by the configured capabilities.
// The caller must supply a system-admin-only route group.
func (h *UserHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/users", h.listUsers)
	if h.manager != nil {
		rg.POST("/users", h.createUser)
		rg.DELETE("/users/:id", h.deleteUser)
	}
}

// RegisterBootstrapRoutes mounts the unauthenticated first-run setup routes.
// The caller must supply a public route group (no access token required) —
// bootstrap() enforces its own one-time-only guard by checking that zero
// users exist, so it stays safe to expose without session auth. This exists
// so a fresh deployment doesn't require shell/kubectl-exec access to the
// server process just to create the very first admin account.
func (h *UserHandler) RegisterBootstrapRoutes(rg *gin.RouterGroup) {
	rg.GET("/auth/bootstrap-status", h.bootstrapStatus)
	if h.manager != nil {
		rg.POST("/auth/bootstrap", h.bootstrap)
	}
}

func (h *UserHandler) bootstrapStatus(c *gin.Context) {
	if h.manager == nil {
		c.JSON(http.StatusOK, gin.H{"required": false})
		return
	}
	users, err := h.directory.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"required": len(users) == 0})
}

// bootstrap creates the first system-admin account. It only succeeds once —
// once any user exists, it always returns 409, matching the CLI's
// `piper user create` in every other way except not requiring a terminal.
func (h *UserHandler) bootstrap(c *gin.Context) {
	h.bootstrapMu.Lock()
	defer h.bootstrapMu.Unlock()

	users, err := h.directory.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(users) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "setup already completed; sign in or ask an existing admin to create your account"})
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.manager.CreateUser(c.Request.Context(), security.CreateUserInput{
		Email:       req.Email,
		Password:    req.Password,
		SystemAdmin: true,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, securityUserView(u))
}

func (h *UserHandler) listUsers(c *gin.Context) {
	users, err := h.directory.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]userViewDTO, len(users))
	for i, u := range users {
		out[i] = securityUserView(u)
	}
	c.JSON(http.StatusOK, out)
}

func (h *UserHandler) createUser(c *gin.Context) {
	var req struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		SystemAdmin bool   `json:"system_admin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.manager.CreateUser(c.Request.Context(), security.CreateUserInput{
		Email:       req.Email,
		Password:    req.Password,
		SystemAdmin: req.SystemAdmin,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, securityUserView(u))
}

func (h *UserHandler) deleteUser(c *gin.Context) {
	id := c.Param("id")
	if err := h.manager.DeleteUser(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── DTO helpers ───────────────────────────────────────────────────────────────

type userViewDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	SystemAdmin bool   `json:"system_admin"`
	Disabled    bool   `json:"disabled"`
}

func persistedUserView(u *User) userViewDTO {
	return userViewDTO{
		ID:          u.ID,
		Email:       u.Email,
		SystemAdmin: u.SystemAdmin,
		Disabled:    u.Disabled,
	}
}

func securityUserView(u *security.User) userViewDTO {
	return userViewDTO{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		SystemAdmin: u.SystemAdmin,
		Disabled:    u.Disabled,
	}
}
