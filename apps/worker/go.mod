// Worker service for the AI Dev Control Plane.
//
// The worker connects to NATS JetStream and processes events for task lifecycle
// management, agent run execution, reviews, approvals, and PR creation.
module github.com/ai-dev-control-plane/worker

go 1.25.0

require (
	github.com/ai-dev-control-plane/api v0.0.0
	github.com/ai-dev-control-plane/db v0.0.0
	github.com/ai-dev-control-plane/events v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/prfactory v0.0.0
	github.com/ai-dev-control-plane/reviewer v0.0.0
	github.com/ai-dev-control-plane/runtimes v0.0.0
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.24
	github.com/nats-io/nats.go v1.37.0
)

require (
	github.com/ai-dev-control-plane/gateway v0.0.0 // indirect
	github.com/ai-dev-control-plane/policies v0.0.0 // indirect
	github.com/ai-dev-control-plane/securityscan v0.0.0 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pressly/goose/v3 v3.23.0 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/oauth2 v0.24.0 // indirect
	golang.org/x/sync v0.9.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

replace (
	github.com/ai-dev-control-plane/api => ../../apps/api
	github.com/ai-dev-control-plane/db => ../../packages/db
	github.com/ai-dev-control-plane/events => ../../packages/events
	github.com/ai-dev-control-plane/gateway => ../../packages/gateway
	github.com/ai-dev-control-plane/models => ../../packages/models
	github.com/ai-dev-control-plane/policies => ../../packages/policies
	github.com/ai-dev-control-plane/prfactory => ../../packages/prfactory
	github.com/ai-dev-control-plane/reviewer => ../../packages/reviewer
	github.com/ai-dev-control-plane/runtimes => ../../packages/runtimes
	github.com/ai-dev-control-plane/securityscan => ../../packages/securityscan
)
