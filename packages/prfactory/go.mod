// Package prfactory creates pull requests for completed agent tasks.
//
// The Factory loads task data, review reports, and workspace information to build
// comprehensive PR descriptions and create GitHub pull requests.
module github.com/ai-dev-control-plane/prfactory

go 1.23.0

require (
	github.com/ai-dev-control-plane/gateway v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/reviewer v0.0.0
	github.com/google/uuid v1.6.0
)

require golang.org/x/oauth2 v0.27.0

replace (
	github.com/ai-dev-control-plane/gateway => ../gateway
	github.com/ai-dev-control-plane/models => ../models
	github.com/ai-dev-control-plane/reviewer => ../reviewer
)
