package apikey

import "errors"

var (
	ErrNotFound      = errors.New("apikey: not found")
	ErrForbidden     = errors.New("apikey: forbidden")
	ErrInactive      = errors.New("apikey: inactive resource")
	ErrInvalidInput  = errors.New("apikey: invalid input")
	ErrConflict      = errors.New("apikey: conflict")
	ErrRevoked       = errors.New("apikey: revoked")
	ErrExpired       = errors.New("apikey: expired")
	ErrInvalidSecret = errors.New("apikey: invalid secret")
)
