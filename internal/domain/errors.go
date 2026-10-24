package domain

import "errors"

var (
	ErrNotFound      = errors.New("resource not found")
	ErrUnauthorized  = errors.New("unauthorized access")
	ErrInvalidInput  = errors.New("invalid input")
	ErrAlreadyExists = errors.New("resource already exists")
	// ErrInUse: delete refused because other records still depend on this one.
	// Wrap it with a message the admin can act on; handlers answer 409.
	ErrInUse = errors.New("still in use")
)
