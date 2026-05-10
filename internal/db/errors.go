package db

import "errors"

var (
	ErrNotFound        = errors.New("not found")
	ErrVersionConflict = errors.New("version conflict")
	ErrNoRefreshToken  = errors.New("no refresh token")
	ErrStorageCap      = errors.New("storage cap reached")
	ErrTransferCap     = errors.New("transfer cap reached")
)
