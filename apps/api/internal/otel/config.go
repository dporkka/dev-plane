package otel

import "time"

const (
	// shutdownTimeout is the maximum time allowed for provider shutdown.
	shutdownTimeout = 5 * time.Second
)
