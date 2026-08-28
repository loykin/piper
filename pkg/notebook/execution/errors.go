package execution

import (
	"errors"
	"fmt"
)

// ErrNotFound and ErrConflict follow the same sentinel-error convention
// pkg/notebook uses (pkg/notebook/errors.go) for resource-not-found and
// generic conflict cases that don't need a structured error code.
var (
	ErrNotFound = errors.New("execution resource not found")
	ErrConflict = errors.New("execution resource conflict")
)

// Error is a structured domain error carrying one of the stable error codes
// from model.go's ErrCode* constants and whether the caller should retry —
// deliberately HTTP-agnostic (no status code) so this package stays free of
// any transport type, per design doc §4.1's package-boundary rule. The REST
// handler (handler.go's writeExecutionError) maps Code to an HTTP status,
// the same {"error":...,"code":...,"retryable":...} envelope convention
// pkg/pipeline/run/handler.go's writeMemberError and
// internal/memberclient/errors.go use elsewhere in this codebase. Message
// must never contain a Jupyter token, endpoint, or raw internal response
// body (design doc §11.3).
type Error struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string { return e.Message }

func newErr(code string, retryable bool, format string, args ...any) *Error {
	return &Error{Code: code, Retryable: retryable, Message: fmt.Sprintf(format, args...)}
}
