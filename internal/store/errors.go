package store

import "errors"

var (
	ErrNotFound = errors.New("not found")
	ErrTooLarge = errors.New("content too large")
)
