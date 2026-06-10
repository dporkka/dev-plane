// Package reviewer performs code review on agent-generated changes.
//
// The Reviewer analyzes git diffs, test results, and agent step history to produce
// a structured review report.
module github.com/ai-dev-control-plane/reviewer

go 1.23

require (
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/securityscan v0.0.0
	github.com/google/uuid v1.6.0
)

replace (
	github.com/ai-dev-control-plane/models => ../models
	github.com/ai-dev-control-plane/securityscan => ../securityscan
)
