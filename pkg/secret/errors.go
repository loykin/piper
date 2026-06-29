package secret

import "errors"

var (
	ErrAlreadyExists = errors.New("secret already exists")
	ErrDisabled      = errors.New("secret is disabled")
	ErrInvalid       = errors.New("invalid secret request")
	ErrNotFound      = errors.New("secret not found")
)
