// Package reviewer performs code review on agent-generated changes.
//
// The Reviewer analyzes git diffs, test results, and agent step history to produce
// a structured review report.
module github.com/ai-dev-control-plane/reviewer

go 1.25.11

require (
	github.com/ai-dev-control-plane/artifacts v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/runtimes v0.0.0
	github.com/ai-dev-control-plane/securityscan v0.0.0
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.47
)

replace (
	github.com/ai-dev-control-plane/artifacts => ../artifacts
	github.com/ai-dev-control-plane/models => ../models
	github.com/ai-dev-control-plane/runtimes => ../runtimes
	github.com/ai-dev-control-plane/securityscan => ../securityscan
)
