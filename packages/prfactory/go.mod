// Package prfactory publishes review changes for completed agent tasks.
//
// The Factory loads task data, review reports, and workspace information to build
// comprehensive review descriptions and open them through a forge provider.
module github.com/ai-dev-control-plane/prfactory

go 1.25.11

require (
	github.com/ai-dev-control-plane/forge v0.0.0
	github.com/ai-dev-control-plane/gateway v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/reviewer v0.0.0
	github.com/ai-dev-control-plane/vcs v0.0.0
	github.com/google/uuid v1.6.0
)

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
)

require github.com/ai-dev-control-plane/securityscan v0.0.0 // indirect

replace (
	github.com/ai-dev-control-plane/forge => ../forge
	github.com/ai-dev-control-plane/gateway => ../gateway
	github.com/ai-dev-control-plane/models => ../models
	github.com/ai-dev-control-plane/reviewer => ../reviewer
	github.com/ai-dev-control-plane/vcs => ../vcs
	github.com/ai-dev-control-plane/securityscan => ../securityscan
)
