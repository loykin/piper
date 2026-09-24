package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLookupFailed(t *testing.T) {
	errMissing := errors.New("missing")
	for _, tc := range []struct {
		name      string
		err       error
		found     bool
		wantWrote bool
		wantCode  int
	}{
		{"found", nil, true, false, 0},
		{"nil result", nil, false, true, http.StatusNotFound},
		{"not-found sentinel", errMissing, false, true, http.StatusNotFound},
		{"wrapped sentinel", errors.Join(errors.New("ctx"), errMissing), false, true, http.StatusNotFound},
		{"database error", errors.New("connection refused"), false, true, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			if wrote := LookupFailed(c, tc.err, tc.found, errMissing, "thing not found"); wrote != tc.wantWrote {
				t.Fatalf("wrote = %v, want %v", wrote, tc.wantWrote)
			}
			if tc.wantWrote && rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}
