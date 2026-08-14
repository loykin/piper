package notebook

import "errors"

var (
	ErrNotFound = errors.New("notebook resource not found")
	ErrConflict = errors.New("notebook resource conflict")
)
