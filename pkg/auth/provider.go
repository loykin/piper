// Package auth provides Piper's built-in authentication capabilities.
// It implements password login, request authentication, authorization, user
// management, and project membership using Argon2id password hashing,
// HMAC-SHA256 signed JWTs, and opaque refresh tokens stored as SHA-256 hashes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/piper/piper/pkg/security"
)

// Config holds configuration for the built-in auth provider.
type Config struct {
	// SigningKey is the HMAC key used to sign access JWTs. Required.
	SigningKey []byte
	// Issuer is the JWT iss claim value. Defaults to "piper".
	Issuer string
	// AccessTTL is how long an access token lives. Default: 15 minutes.
	AccessTTL time.Duration
	// RefreshTTL is how long a refresh token lives. Default: 7 days.
	RefreshTTL time.Duration
}

func (c *Config) withDefaults() {
	if c.Issuer == "" {
		c.Issuer = "piper"
	}
	if c.AccessTTL == 0 {
		c.AccessTTL = 15 * time.Minute
	}
	if c.RefreshTTL == 0 {
		c.RefreshTTL = 7 * 24 * time.Hour
	}
}

// Provider implements Piper's built-in authentication and identity capabilities.
type Provider struct {
	cfg      Config
	users    UserRepository
	members  security.ProjectMemberRepository
	sessions SessionRepository
}

var (
	_ LoginProvider                 = (*Provider)(nil)
	_ security.Authenticator        = (*Provider)(nil)
	_ security.Authorizer           = (*Provider)(nil)
	_ security.UserDirectory        = (*Provider)(nil)
	_ security.UserManager          = (*Provider)(nil)
	_ security.ProjectMemberManager = (*Provider)(nil)
)

// LoginProvider implements the built-in login and session protocol.
type LoginProvider interface {
	Login(ctx context.Context, email, password string) (*LoginResult, error)
	Refresh(ctx context.Context, refreshToken string) (*LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
	RevokeAllSessions(ctx context.Context, userID string) error
	AccessTTL() time.Duration
}

// New creates a Provider. All three repositories are required.
func New(cfg Config, users UserRepository, members security.ProjectMemberRepository, sessions SessionRepository) *Provider {
	cfg.withDefaults()
	if len(cfg.SigningKey) == 0 {
		panic("auth.Provider: SigningKey must not be empty")
	}
	return &Provider{cfg: cfg, users: users, members: members, sessions: sessions}
}

// ── security.Authenticator / security.Authorizer ──────────────────────────

// Authenticate reads the access JWT from Authorization header or the piper_access cookie.
func (p *Provider) Authenticate(ctx context.Context, r *http.Request) (*security.Identity, error) {
	token := bearerToken(r)
	if token == "" {
		if c, err := r.Cookie("piper_access"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		return nil, nil // anonymous request
	}
	claims, err := parseAccessToken(token, p.cfg.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("invalid access token: %w", err)
	}
	u, err := p.users.GetByID(ctx, claims.UserID)
	if err != nil || u == nil || u.Disabled {
		return nil, fmt.Errorf("user not found or disabled")
	}
	return userToIdentity(u), nil
}

func (p *Provider) ListProjectRoles(ctx context.Context, identity *security.Identity) (map[string]security.ProjectRole, error) {
	if identity == nil {
		return nil, nil
	}
	if identity.SystemAdmin {
		return nil, nil // caller should treat nil as "all projects visible"
	}
	memberships, err := p.members.ListByUser(ctx, identity.ID)
	if err != nil {
		return nil, err
	}
	roles := make(map[string]security.ProjectRole, len(memberships))
	for _, m := range memberships {
		roles[m.ProjectID] = roleStringToProjectRole(m.Role)
	}
	return roles, nil
}

func (p *Provider) ProjectRole(ctx context.Context, identity *security.Identity, projectID string) (security.ProjectRole, error) {
	if identity == nil {
		return 0, errors.New("authentication required")
	}
	if identity.SystemAdmin {
		return security.ProjectRoleAdmin, nil
	}
	m, err := p.members.Get(ctx, projectID, identity.ID)
	if err != nil || m == nil {
		return 0, fmt.Errorf("no access to project %q", projectID)
	}
	return roleStringToProjectRole(m.Role), nil
}

func (p *Provider) AuthorizeSystem(_ context.Context, identity *security.Identity) error {
	if identity == nil {
		return errors.New("authentication required")
	}
	if !identity.SystemAdmin {
		return errors.New("system admin access required")
	}
	return nil
}

// ── Login / Refresh / Logout ──────────────────────────────────────────────

// LoginResult holds the tokens returned on successful login.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	User         *User
}

// Login authenticates with email+password and issues a new session.
func (p *Provider) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	u, err := p.users.GetByEmail(ctx, email)
	if err != nil || u == nil {
		return nil, errors.New("invalid credentials")
	}
	if u.Disabled {
		return nil, errors.New("account disabled")
	}
	if !verifyPassword(password, u.PasswordHash) {
		return nil, errors.New("invalid credentials")
	}
	return p.createSession(ctx, u)
}

// Refresh rotates a refresh token and issues a new access+refresh pair.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	hash := hashToken(refreshToken)
	s, err := p.sessions.GetByTokenHash(ctx, hash)
	if err != nil || s == nil {
		return nil, errors.New("invalid refresh token")
	}
	if s.RevokedAt != nil {
		return nil, errors.New("session revoked")
	}
	if time.Now().After(s.ExpiresAt) {
		return nil, errors.New("session expired")
	}

	// Revoke old session before issuing a new one (rotation).
	now := time.Now().UTC()
	if err := p.sessions.Revoke(ctx, s.ID, now); err != nil {
		return nil, fmt.Errorf("revoke old session: %w", err)
	}

	u, err := p.users.GetByID(ctx, s.UserID)
	if err != nil || u == nil || u.Disabled {
		return nil, errors.New("user not found or disabled")
	}
	return p.createSession(ctx, u)
}

// Logout revokes the session associated with the given refresh token.
func (p *Provider) Logout(ctx context.Context, refreshToken string) error {
	hash := hashToken(refreshToken)
	s, err := p.sessions.GetByTokenHash(ctx, hash)
	if err != nil || s == nil {
		return nil
	}
	return p.sessions.Revoke(ctx, s.ID, time.Now().UTC())
}

// ── User management ───────────────────────────────────────────────────────

// CreateUser creates a new user account.
func (p *Provider) CreateUser(ctx context.Context, input security.CreateUserInput) (*security.User, error) {
	hash, err := hashPassword(input.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC()
	u := &User{
		ID:           uuid.NewString(),
		Email:        strings.ToLower(strings.TrimSpace(input.Email)),
		PasswordHash: hash,
		SystemAdmin:  input.SystemAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := p.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return userView(u), nil
}

func (p *Provider) ListUsers(ctx context.Context) ([]*security.User, error) {
	users, err := p.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*security.User, len(users))
	for i, user := range users {
		out[i] = userView(user)
	}
	return out, nil
}

func (p *Provider) DeleteUser(ctx context.Context, id string) error {
	if err := p.sessions.RevokeAll(ctx, id); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return p.users.Delete(ctx, id)
}

func (p *Provider) RevokeAllSessions(ctx context.Context, userID string) error {
	return p.sessions.RevokeAll(ctx, userID)
}

func (p *Provider) GetUser(ctx context.Context, userID string) (*security.User, error) {
	user, err := p.users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, err
	}
	return userView(user), nil
}

func (p *Provider) ListMembers(ctx context.Context, projectID string) ([]*security.ProjectMember, error) {
	return p.members.ListByProject(ctx, projectID)
}

func (p *Provider) AddMember(ctx context.Context, member *security.ProjectMember) error {
	return p.members.Add(ctx, member)
}

func (p *Provider) GetMember(ctx context.Context, projectID, userID string) (*security.ProjectMember, error) {
	return p.members.Get(ctx, projectID, userID)
}

func (p *Provider) UpdateMember(ctx context.Context, member *security.ProjectMember) error {
	return p.members.Update(ctx, member)
}

func (p *Provider) RemoveMember(ctx context.Context, projectID, userID string) error {
	return p.members.Remove(ctx, projectID, userID)
}

func (p *Provider) AccessTTL() time.Duration {
	return p.cfg.AccessTTL
}

// ── helpers ───────────────────────────────────────────────────────────────

func (p *Provider) createSession(ctx context.Context, u *User) (*LoginResult, error) {
	accessToken, err := issueAccessToken(u, p.cfg.SigningKey, p.cfg.Issuer, p.cfg.AccessTTL)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}
	refreshToken, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	now := time.Now().UTC()
	s := &Session{
		ID:               uuid.NewString(),
		UserID:           u.ID,
		RefreshTokenHash: hashToken(refreshToken),
		ExpiresAt:        now.Add(p.cfg.RefreshTTL),
		CreatedAt:        now,
		LastUsedAt:       now,
	}
	if err := p.sessions.Create(ctx, s); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    s.ExpiresAt,
		User:         u,
	}, nil
}

func userToIdentity(u *User) *security.Identity {
	return &security.Identity{
		ID:          u.ID,
		Email:       u.Email,
		SystemAdmin: u.SystemAdmin,
	}
}

func userView(u *User) *security.User {
	return &security.User{
		ID:          u.ID,
		Email:       u.Email,
		SystemAdmin: u.SystemAdmin,
		Disabled:    u.Disabled,
	}
}

func roleStringToProjectRole(role string) security.ProjectRole {
	switch role {
	case "admin":
		return security.ProjectRoleAdmin
	case "member":
		return security.ProjectRoleMember
	default:
		return security.ProjectRoleViewer
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}
