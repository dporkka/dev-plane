package gateway

import "context"

// ForgePullRequest is the provider-neutral pull request result used by Dev Plane.
type ForgePullRequest struct {
	Number  int
	HTMLURL string
	State   string
	Draft   bool
}

// ForgeMergeResult is the provider-neutral result of merging a pull request.
type ForgeMergeResult struct {
	SHA     string
	Merged  bool
	Message string
}

// ForgeClient is the minimal forge contract needed by agent PR workflows.
type ForgeClient interface {
	CreatePullRequest(ctx context.Context, token, owner, name string, pr NewPR) (*ForgePullRequest, error)
	MergePullRequest(ctx context.Context, token, owner, name string, number int, req MergePRRequest) (*ForgeMergeResult, error)
}
