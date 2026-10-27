package deployment

import "errors"

var (
	ErrNotFound             = errors.New("deployment: not found")
	ErrForbidden            = errors.New("deployment: forbidden")
	ErrInactive             = errors.New("deployment: inactive resource")
	ErrInvalidInput         = errors.New("deployment: invalid input")
	ErrConflict             = errors.New("deployment: conflict")
	ErrInvalidTransition    = errors.New("deployment: invalid transition")
	ErrNoActiveConfig       = errors.New("deployment: no active configuration version")
	ErrConfigMismatch       = errors.New("deployment: configuration version mismatch")
	ErrVersionNotDeployable = errors.New("deployment: configuration version is not deployable")
	ErrProviderUnavailable  = errors.New("deployment: provider unavailable")
	ErrStaleWorker          = errors.New("deployment: stale worker")
	ErrReadyWithoutEvidence = errors.New("deployment: ready requires validated provider evidence")
)
