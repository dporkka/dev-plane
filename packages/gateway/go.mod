// Package gateway provides integrations with external services including
// GitHub OAuth, repository operations, webhook validation, and pull request management.
module github.com/ai-dev-control-plane/gateway

go 1.25.11

require (
	github.com/ai-dev-control-plane/forge v0.0.0
	golang.org/x/oauth2 v0.36.0
)

replace github.com/ai-dev-control-plane/forge => ../forge
