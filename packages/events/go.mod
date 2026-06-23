// Package events provides a NATS JetStream event bus for inter-service communication.
//
// The Bus wraps a NATS connection and JetStream context, providing typed
// stream creation, publish/subscribe, and graceful shutdown.
module github.com/ai-dev-control-plane/events

go 1.25.11

require github.com/nats-io/nats.go v1.52.0

require (
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
