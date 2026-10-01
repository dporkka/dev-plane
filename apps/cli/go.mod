module github.com/ai-dev-control-plane/cli

go 1.25.11

require (
	github.com/ai-dev-control-plane/readiness v0.0.0
	github.com/ai-dev-control-plane/repoprotocol v0.0.0
	github.com/ai-dev-control-plane/runtimes v0.0.0
	github.com/ai-dev-control-plane/verifier v0.0.0
)

replace (
	github.com/ai-dev-control-plane/readiness => ../../packages/readiness
	github.com/ai-dev-control-plane/repoprotocol => ../../packages/repoprotocol
	github.com/ai-dev-control-plane/runtimes => ../../packages/runtimes
	github.com/ai-dev-control-plane/verifier => ../../packages/verifier
)
