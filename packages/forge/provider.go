// Package forge defines provider-neutral code-forge operations used by Dev Plane.
package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Common provider errors.
var (
	ErrInvalidRequest = errors.New("invalid forge request")
	ErrNotFound       = errors.New("forge resource not found")
	ErrConflict       = errors.New("forge conflict")
)

// Credential contains the API credential used for forge operations.
// The provider decides how the token is presented to its upstream API.
type Credential struct {
	Token string
}

// Repository identifies a repository within a forge.
type Repository struct {
	Namespace string
	Name      string
}

// ChangeState is the provider-neutral lifecycle state of a code change request.
type ChangeState string

const (
	ChangeStateOpen   ChangeState = "open"
	ChangeStateClosed ChangeState = "closed"
	ChangeStateMerged ChangeState = "merged"
)

// MergeMethod identifies a common forge merge strategy.
type MergeMethod string

const (
	MergeMethodMerge  MergeMethod = "merge"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodRebase MergeMethod = "rebase"
)

// OpenChangeRequest describes a request to open a code change review.
type OpenChangeRequest struct {
	Title string
	Body  string
	Head  string
	Base  string
	Draft bool
}

// Change is the provider-neutral representation of an opened code change.
type Change struct {
	Number       int
	Title        string
	Body         string
	URL          string
	State        ChangeState
	Head         string
	Base         string
	HeadRevision string
	Draft        bool
}

// MergeChangeRequest describes how an existing code change should be merged.
type MergeChangeRequest struct {
	Method               MergeMethod
	CommitTitle          string
	CommitMessage        string
	ExpectedHeadRevision string
}

// MergeResult is the provider-neutral result of a merge request.
type MergeResult struct {
	Merged   bool
	Revision string
	Message  string
}

// Provider opens and merges code change requests on a source-code forge.
type Provider interface {
	Name() string
	OpenChange(ctx context.Context, credential Credential, repository Repository, req OpenChangeRequest) (*Change, error)
	MergeChange(ctx context.Context, credential Credential, repository Repository, number int, req MergeChangeRequest) (*MergeResult, error)
}

// ValidateOpenChangeRequest validates the provider-neutral requirements shared by all forges.
func ValidateOpenChangeRequest(repository Repository, req OpenChangeRequest) error {
	if strings.TrimSpace(repository.Namespace) == "" || strings.TrimSpace(repository.Name) == "" {
		return fmt.Errorf("%w: repository owner and name are required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Head) == "" {
		return fmt.Errorf("%w: head branch is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Base) == "" {
		return fmt.Errorf("%w: base branch is required", ErrInvalidRequest)
	}
	return nil
}

// ValidateMergeChangeRequest validates the provider-neutral requirements shared by all forges.
func ValidateMergeChangeRequest(repository Repository, number int, req MergeChangeRequest) error {
	if strings.TrimSpace(repository.Namespace) == "" || strings.TrimSpace(repository.Name) == "" {
		return fmt.Errorf("%w: repository owner and name are required", ErrInvalidRequest)
	}
	if number <= 0 {
		return fmt.Errorf("%w: change number must be positive", ErrInvalidRequest)
	}
	switch req.Method {
	case "", MergeMethodMerge, MergeMethodSquash, MergeMethodRebase:
		return nil
	default:
		return fmt.Errorf("%w: unsupported merge method %q", ErrInvalidRequest, req.Method)
	}
}

// NormalizeMergeMethod returns the canonical default merge method.
func NormalizeMergeMethod(method MergeMethod) MergeMethod {
	if method == "" {
		return MergeMethodMerge
	}
	return method
}

// IsInvalidRequest reports whether err represents invalid provider-neutral input.
func IsInvalidRequest(err error) bool {
	return errors.Is(err, ErrInvalidRequest)
}
