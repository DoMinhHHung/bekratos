package configversion

import (
	"context"

	"github.com/google/uuid"
)

type ApplyRequest struct {
	EnvironmentID         uuid.UUID
	VersionID             uuid.UUID
	ManifestFormatVersion int
	ManifestSHA256        string
	ManifestCanonical     string
	ApplyRequestID        string
}

type ApplyResult struct {
	Accepted       bool
	AppliedVersion uuid.UUID
	AppliedSHA256  string
	Code           string
	Message        string
}

type ApplyAdapter interface {
	Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error)
}

type UnavailableApplyAdapter struct{}

func NewUnavailableApplyAdapter() *UnavailableApplyAdapter {
	return &UnavailableApplyAdapter{}
}

func (a *UnavailableApplyAdapter) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	return ApplyResult{
		Accepted: false,
		Code:     "runtime_unavailable",
		Message:  "runtime configuration apply contract is not configured",
	}, ErrRuntimeUnavailable
}

type RecordingApplyAdapter struct {
	Result ApplyResult
	Err    error
	Calls  []ApplyRequest
}

func (a *RecordingApplyAdapter) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	a.Calls = append(a.Calls, req)
	if a.Err != nil {
		return ApplyResult{}, a.Err
	}
	return a.Result, nil
}
