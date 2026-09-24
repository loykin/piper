package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// InternalError answers 500 for an unexpected failure. The full error goes to
// the server log with a short reference id; the response carries only
// `message` (default "internal error") and that reference, e.g.
// {"error":"internal error (ref 3f9a2c1b)"}. Raw errors — SQL, file paths,
// upstream response bodies — stay out of the API, and an operator still gets
// the whole cause by searching the log for the ref the user reports.
func InternalError(c *gin.Context, err error, message ...string) {
	msg := "internal error"
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	ref := newRef()
	attrs := []any{"ref", ref, "route", c.FullPath(), "err", err}
	if c.Request != nil {
		attrs = append(attrs, "method", c.Request.Method, "path", c.Request.URL.Path)
	}
	slog.Error(msg, attrs...)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": msg + " (ref " + ref + ")"})
}

func newRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
