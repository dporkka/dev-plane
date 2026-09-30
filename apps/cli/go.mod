module github.com/ai-dev-control-plane/cli

go 1.25.11

require (
	github.com/ai-dev-control-plane/repo-manifest v0.0.0
	github.com/ai-dev-control-plane/verification v0.0.0
)

replace github.com/ai-dev-control-plane/repo-manifest => ../../packages/repo-manifest

replace github.com/ai-dev-control-plane/verification => ../../packages/verification
