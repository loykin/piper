package mlflow

import (
	"errors"
	"strconv"
	"time"
)

var (
	ErrNotFound      = errors.New("mlflow integration not found")
	ErrAlreadyExists = errors.New("mlflow integration already exists")
	ErrInvalid       = errors.New("invalid mlflow integration request")
)

// ClientError is returned by the real HTTP Client (http_client.go) for
// every failed MLflow Tracking REST call — both remote HTTP-level failures
// (status >= 300, with a small parsed error_code/message per MLflow's
// error envelope) and network/transport failures (DNS, connect, TLS,
// timeout, or this package's own SSRF guard rejecting an address). Message
// is always redacted per design doc section 15.2: never the raw response
// body, never a credential, never a full stack trace.
type ClientError struct {
	// StatusCode is the remote HTTP status, or 0 for a network-level
	// failure that never got a response.
	StatusCode int
	// Code is MLflow's own error_code field (e.g. "RESOURCE_DOES_NOT_EXIST",
	// "INVALID_PARAMETER_VALUE", "RESOURCE_ALREADY_EXISTS"), when the
	// response body was a well-formed MLflow error envelope. May be empty.
	Code string
	// Message is a short, redacted, human-readable description — safe to
	// log and safe to surface through the REST API (design doc section
	// 11.1's connection-test result, for instance).
	Message string
	// RetryAfter, when > 0, is parsed from the response's Retry-After
	// header (design doc section 10.2).
	RetryAfter time.Duration
	retryable  bool
}

func (e *ClientError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return e.Message
}

// Retryable reports whether the Dispatcher should retry this error with
// backoff (design doc section 10.2's retryable/non-retryable lists):
// network timeout/reset, HTTP 408/425/429, and 5xx are retryable; 401/403,
// malformed endpoint/schema, and validation/length errors are not.
func (e *ClientError) Retryable() bool {
	return e != nil && e.retryable
}

// IsRetryable reports whether err should be retried by an outbox
// Dispatcher, per design doc section 10.2. A non-*ClientError (a bug, or a
// Handler-internal error like a JSON decode failure on our own payload) is
// treated as non-retryable — retrying a programming error indefinitely
// would just hide it.
func IsRetryable(err error) bool {
	var ce *ClientError
	if errors.As(err, &ce) {
		return ce.Retryable()
	}
	return false
}

// ErrorCode returns the MLflow error_code (or a short synthetic code for a
// network-level failure) for use in outbox.Outcome.ErrorCode /
// MLflowRunLink.LastErrorCode. Never empty for a *ClientError.
func ErrorCode(err error) string {
	var ce *ClientError
	if errors.As(err, &ce) {
		if ce.Code != "" {
			return ce.Code
		}
		if ce.StatusCode > 0 {
			return "HTTP_" + strconv.Itoa(ce.StatusCode)
		}
		return "NETWORK_ERROR"
	}
	return "INTERNAL_ERROR"
}
