package configversion

import "errors"

var (
	ErrNotFound             = errors.New("configversion: not found")
	ErrForbidden            = errors.New("configversion: forbidden")
	ErrInactive             = errors.New("configversion: inactive resource")
	ErrInvalidInput         = errors.New("configversion: invalid input")
	ErrConflict             = errors.New("configversion: conflict")
	ErrConflictRevision     = errors.New("configversion: revision conflict")
	ErrConflictIdempotency  = errors.New("configversion: idempotency key conflict")
	ErrValidation           = errors.New("configversion: validation")
	ErrImmutable            = errors.New("configversion: immutable")
	ErrInvalidTransition    = errors.New("configversion: invalid state transition")
	ErrRuntimeUnavailable   = errors.New("configversion: runtime unavailable")
	ErrStaleWorker          = errors.New("configversion: stale worker fencing")
	ErrConfirmationRequired = errors.New("configversion: confirmation required")
	ErrConfirmationMismatch = errors.New("configversion: confirmation mismatch")
	ErrNotEligible          = errors.New("configversion: not eligible")
)
