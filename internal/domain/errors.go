package domain

import "errors"

// common domain errors, mapped to http status in delivery layer
var (
	ErrNotFound     = errors.New("resource not found")
	ErrConflict     = errors.New("resource already exists")
	ErrInvalidInput = errors.New("invalid input")
	ErrUnavailable  = errors.New("service unavailable")
	ErrInternal     = errors.New("internal server error")
)
