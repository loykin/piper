package security

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RespondUnauthorized writes a 401 response for a request that carries no
// valid identity at all, and aborts the gin context. msg defaults to
// "authentication required" when empty.
//
// Callers must pick 401 vs. 403 based on whether an identity was resolved,
// never on the outcome of a role/permission check — conflating the two hides
// "you're not logged in" behind "forbidden", which silently defeats a
// frontend's 401-triggered re-authentication flow.
func RespondUnauthorized(c *gin.Context, msg string) {
	if msg == "" {
		msg = "authentication required"
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": msg})
	c.Abort()
}

// RespondForbidden writes a 403 response for a caller with a resolved
// identity that lacks the required role/permission, and aborts the gin
// context.
func RespondForbidden(c *gin.Context, msg string) {
	c.JSON(http.StatusForbidden, gin.H{"error": msg})
	c.Abort()
}
