package connection

import "errors"

var (
	ErrAlreadyExists  = errors.New("connection already exists")
	ErrDisabled       = errors.New("connection is disabled")
	ErrInvalid        = errors.New("invalid connection request")
	ErrNotFound       = errors.New("connection not found")
	ErrScopeViolation = errors.New("repo URL is outside the connection's endpoint scope")
)
