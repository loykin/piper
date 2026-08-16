// Package httpx holds small Gin-request helpers shared across domain
// handlers, so every list endpoint reads and reports pagination the same
// way rather than each package inventing its own query-param parsing.
package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// ParseLimitOffset reads limit/offset query params using the app-wide
// convention: limit must be a positive integer to activate pagination at
// all — offset only applies when limit > 0. Returns 0, 0 (unbounded) when
// no limit was requested, matching pkg/pipeline/run.RunFilter's Limit/Offset
// contract.
func ParseLimitOffset(c *gin.Context) (limit, offset int) {
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
		if o, err := strconv.Atoi(c.Query("offset")); err == nil && o > 0 {
			offset = o
		}
	}
	return limit, offset
}

// SetTotalCountHeader sets X-Total-Count when limit > 0 — the header is
// only meaningful for a paginated request, mirroring the existing Run
// History endpoint's behavior.
func SetTotalCountHeader(c *gin.Context, limit, total int) {
	if limit > 0 {
		c.Header("X-Total-Count", strconv.Itoa(total))
	}
}
