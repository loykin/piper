package httpx

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInternalErrorKeepsDetailsInTheLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	InternalError(c, errors.New(`pq: relation "secret_table" does not exist`), "failed to load things")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "secret_table") {
		t.Fatalf("response leaks the raw error: %s", body)
	}
	m := regexp.MustCompile(`failed to load things \(ref ([0-9a-f]{8})\)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("response = %s, want message with a ref", body)
	}
	if !strings.Contains(logs.String(), "ref="+m[1]) || !strings.Contains(logs.String(), "secret_table") {
		t.Fatalf("log must carry the ref and the raw error, got: %s", logs.String())
	}
}
