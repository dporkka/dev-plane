module github.com/ai-dev-control-plane/verifier

go 1.25.11

require (
	github.com/ai-dev-control-plane/db v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/repoprotocol v0.0.0
	github.com/ai-dev-control-plane/runtimes v0.0.0
)

replace (
	github.com/ai-dev-control-plane/db => ../db
	github.com/ai-dev-control-plane/models => ../models
	github.com/ai-dev-control-plane/repoprotocol => ../repoprotocol
	github.com/ai-dev-control-plane/runtimes => ../runtimes
)
