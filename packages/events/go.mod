// Package events provides a NATS JetStream event bus for inter-service communication.
//
// The Bus wraps a NATS connection and JetStream context, providing typed
// stream creation, publish/subscribe, and graceful shutdown.
module github.com/ai-dev-control-plane/events

go 1.25.0

require github.com/nats-io/nats.go v1.37.0

require (
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)
