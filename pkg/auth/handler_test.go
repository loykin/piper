package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/security"
)

type userDirectoryStub struct{}

func (userDirectoryStub) GetUser(context.Context, string) (*security.User, error) {
	return nil, nil
}

func (userDirectoryStub) ListUsers(context.Context) ([]*security.User, error) {
	return []*security.User{}, nil
}

func TestUserHandlerReadOnlyDirectoryDoesNotRegisterMutations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewUserHandler(userDirectoryStub{}, nil).RegisterRoutes(router.Group("/api"))

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.Code)
	}

	create := httptest.NewRecorder()
	router.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/users", nil))
	if create.Code != http.StatusNotFound {
		t.Fatalf("create status = %d, want 404", create.Code)
	}

	deleteUser := httptest.NewRecorder()
	router.ServeHTTP(deleteUser, httptest.NewRequest(http.MethodDelete, "/api/users/user-1", nil))
	if deleteUser.Code != http.StatusNotFound {
		t.Fatalf("delete status = %d, want 404", deleteUser.Code)
	}
}
