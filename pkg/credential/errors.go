package credential

import "errors"

var (
	ErrAlreadyExists  = errors.New("credential already exists")
	ErrDisabled       = errors.New("credential is disabled")
	ErrInvalid        = errors.New("invalid credential request")
	ErrNotFound       = errors.New("credential not found")
	ErrScopeViolation = errors.New("repo URL is outside the credential's endpoint scope")
)
