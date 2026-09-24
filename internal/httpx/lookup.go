package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// LookupFailed writes the response for a failed single-resource lookup and
// reports whether it did. It follows the repository convention — a missing
// row is (nil, nil), an error is a real failure: `found` false (missing, or
// outside the caller's scope such as another project) is 404 with `message`;
// a non-nil err is a 500 through InternalError. Mapping every lookup error to
// 404 made a database outage read as "deleted" in the UI, which shows "Not
// Found" only for a 404.
func LookupFailed(c *gin.Context, err error, found bool, message string) bool {
	switch {
	case err != nil:
		InternalError(c, err)
	case !found:
		c.JSON(http.StatusNotFound, gin.H{"error": message})
	default:
		return false
	}
	return true
}
