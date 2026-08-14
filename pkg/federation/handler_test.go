package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/loykin/piper/pkg/project"
)

type handlerRepo struct {
	homeID string
	limit  int
}

func (*handlerRepo) SyncConfiguredMembers(context.Context, string, []string, time.Time) error {
	return nil
}
func (*handlerRepo) SetMemberConnected(context.Context, string, string, bool, time.Time) error {
	return nil
}
func (*handlerRepo) CreateProject(context.Context, string, *project.Project, string) error {
	return nil
}
func (*handlerRepo) SetProjectOwner(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (r *handlerRepo) ListMembers(_ context.Context, homeID string) ([]*Member, error) {
	r.homeID = homeID
	return []*Member{{HomeID: homeID, ID: "member-a", Enabled: true, Status: MemberOnline}}, nil
}
func (r *handlerRepo) ListAuditEvents(_ context.Context, homeID string, limit int) ([]*AuditEvent, error) {
	r.homeID = homeID
	r.limit = limit
	return []*AuditEvent{}, nil
}

func TestHandlerScopesDirectoryToHome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &handlerRepo{}
	router := gin.New()
	NewHandler(repo, "home-a", nil).RegisterRoutes(router.Group("/api"))

	members := httptest.NewRecorder()
	router.ServeHTTP(members, httptest.NewRequest(http.MethodGet, "/api/federation/members", nil))
	if members.Code != http.StatusOK || repo.homeID != "home-a" {
		t.Fatalf("members status=%d home=%q body=%s", members.Code, repo.homeID, members.Body.String())
	}

	audit := httptest.NewRecorder()
	router.ServeHTTP(audit, httptest.NewRequest(http.MethodGet, "/api/federation/audit-events?limit=12", nil))
	if audit.Code != http.StatusOK || repo.limit != 12 {
		t.Fatalf("audit status=%d limit=%d body=%s", audit.Code, repo.limit, audit.Body.String())
	}
}

func TestHandlerRejectsInvalidAuditLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(&handlerRepo{}, "home-a", nil).RegisterRoutes(router.Group("/api"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/federation/audit-events?limit=501", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
