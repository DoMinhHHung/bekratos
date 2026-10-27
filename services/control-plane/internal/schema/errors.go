package schema

import "errors"

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrInvalidInput     = errors.New("invalid input")
	ErrConflictRevision = errors.New("revision conflict")
	ErrInactive         = errors.New("inactive")
	ErrValidation       = errors.New("schema validation failed")
	ErrProtected        = errors.New("protected definition")
	ErrDuplicateKey     = errors.New("duplicate key")
	ErrReservedKey      = errors.New("reserved key")
	ErrUnknownEntity    = errors.New("unknown entity")
	ErrUnknownField     = errors.New("unknown field")
	ErrInvalidRelation  = errors.New("invalid relationship")
	ErrInvalidPolicy    = errors.New("invalid policy")
	ErrModuleSource     = errors.New("module source unavailable")
)
