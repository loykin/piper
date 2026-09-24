package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBindJSONMessagesNameTheJSONField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type body struct {
		Type    string `json:"type" binding:"required"`
		MaxRuns int    `json:"max_runs"`
	}
	for _, tc := range []struct{ name, in, want string }{
		{"missing required field", `{}`, "type is required"},
		{"wrong type", `{"type":"x","max_runs":"five"}`, "max_runs has the wrong type (expected int)"},
		{"not JSON", `{`, "request body must be valid JSON"},
		{"empty body", ``, "request body must be valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.in))
			c.Request.Header.Set("Content-Type", "application/json")
			var b body
			if BindJSON(c, &b) {
				t.Fatal("BindJSON accepted an invalid body")
			}
			var resp struct{ Error string }
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if rec.Code != http.StatusBadRequest || resp.Error != tc.want {
				t.Fatalf("got %d %q, want 400 %q", rec.Code, resp.Error, tc.want)
			}
		})
	}
}
