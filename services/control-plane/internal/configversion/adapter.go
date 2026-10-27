package configversion

import (
	"context"

	"github.com/google/uuid"
)

type ApplyRequest struct {
	EnvironmentID           uuid.UUID
	VersionID               uuid.UUID
	ManifestFormatVersion   int
	ManifestSHA256          string
	ManifestCanonical       string
	ApplyRequestID          string
	ExpectedActiveVersionID *uuid.UUID
}

type ApplyResult struct {
	Accepted                bool
	AppliedVersion          uuid.UUID
	AppliedSHA256           string
	EnvironmentID           uuid.UUID
	ExpectedActiveVersionID *uuid.UUID
	Code                    string
	Message                 string
	NeedsReconciliation     bool
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

	Conditional   bool
	CurrentActive *uuid.UUID
}

func (a *RecordingApplyAdapter) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	a.Calls = append(a.Calls, req)
	if a.Err != nil {
		return ApplyResult{}, a.Err
	}
	if a.Conditional {
		if !uuidPtrEqual(a.CurrentActive, req.ExpectedActiveVersionID) {
			return ApplyResult{
				Accepted: false,
				Code:     "stale_base",
				Message:  "expected active version no longer matches runtime base",
			}, nil
		}
	}
	res := a.Result
	if res.EnvironmentID == uuid.Nil {
		res.EnvironmentID = req.EnvironmentID
	}
	if res.AppliedVersion == uuid.Nil {
		res.AppliedVersion = req.VersionID
	}
	if res.AppliedSHA256 == "" {
		res.AppliedSHA256 = req.ManifestSHA256
	}
	if res.ExpectedActiveVersionID == nil {
		res.ExpectedActiveVersionID = req.ExpectedActiveVersionID
	}
	return res, nil
}

type RetryableApplyError struct {
	Code    string
	Message string
}

func (e RetryableApplyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e RetryableApplyError) IsRetryable() bool { return true }
