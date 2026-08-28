package mlflow

import "errors"

var (
	ErrNotFound      = errors.New("mlflow integration not found")
	ErrAlreadyExists = errors.New("mlflow integration already exists")
	ErrInvalid       = errors.New("invalid mlflow integration request")
)
