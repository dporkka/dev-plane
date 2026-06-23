module github.com/ai-dev-control-plane/api

go 1.25.11

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/ai-dev-control-plane/events v0.0.0
	github.com/ai-dev-control-plane/models v0.0.0
	github.com/ai-dev-control-plane/policies v0.0.0
	github.com/ai-dev-control-plane/prfactory v0.0.0
	github.com/ai-dev-control-plane/repo-intel v0.0.0
	github.com/ai-dev-control-plane/reviewer v0.0.0
	github.com/ai-dev-control-plane/runtimes v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/go-chi/cors v1.2.1
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/lib/pq v1.10.9
	github.com/mattn/go-sqlite3 v1.14.24
	github.com/pressly/goose/v3 v3.23.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.40.0
	go.opentelemetry.io/otel/metric v1.40.0
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel/trace v1.40.0
	golang.org/x/oauth2 v0.24.0
)

require (
	github.com/ai-dev-control-plane/gateway v0.0.0 // indirect
	github.com/ai-dev-control-plane/securityscan v0.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/nats-io/nats.go v1.37.0 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/sync v0.9.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

replace (
	github.com/ai-dev-control-plane/db => ../../packages/db
	github.com/ai-dev-control-plane/events => ../../packages/events
	github.com/ai-dev-control-plane/gateway => ../../packages/gateway
	github.com/ai-dev-control-plane/models => ../../packages/models
	github.com/ai-dev-control-plane/policies => ../../packages/policies
	github.com/ai-dev-control-plane/prfactory => ../../packages/prfactory
	github.com/ai-dev-control-plane/repo-intel => ../../packages/repo-intel
	github.com/ai-dev-control-plane/reviewer => ../../packages/reviewer
	github.com/ai-dev-control-plane/runtimes => ../../packages/runtimes
	github.com/ai-dev-control-plane/securityscan => ../../packages/securityscan
)
