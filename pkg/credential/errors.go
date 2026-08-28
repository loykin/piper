package credential

import "errors"

var (
	ErrAlreadyExists  = errors.New("credential already exists")
	ErrDisabled       = errors.New("credential is disabled")
	ErrInvalid        = errors.New("invalid credential request")
	ErrNotFound       = errors.New("credential not found")
	ErrScopeViolation = errors.New("repo URL is outside the credential's endpoint scope")
	// ErrInUse is returned by Delete when a registered InUseChecker reports
	// this credential is still referenced elsewhere (see Store.AddInUseChecker) —
	// e.g. the server's own storage.CredentialRef. Wrapped with the checker's
	// reason string, so callers/logs see *what* still references it.
	ErrInUse = errors.New("credential is still in use")
)
