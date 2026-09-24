package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// LookupFailed writes the response for a failed single-resource lookup and
// reports whether it did. A missing resource — `found` false, or err wrapping
// notFound — is 404 with `message`; any other error is a 500. Mapping every
// lookup error to 404 made a database outage read as "deleted" in the UI,
// which shows "Not Found" only for a 404.
func LookupFailed(c *gin.Context, err error, found bool, notFound error, message string) bool {
	switch {
	case err == nil && found:
		return false
	case err == nil || (notFound != nil && errors.Is(err, notFound)):
		c.JSON(http.StatusNotFound, gin.H{"error": message})
	default:
		InternalError(c, err)
	}
	return true
}
