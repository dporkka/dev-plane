// Package runtimes defines the Provider interface for workspace runtimes and
// provides Docker and local implementations.
//
// A runtime Provider manages the lifecycle of isolated workspaces where agents
// execute commands, read/write files, and apply code patches.
module github.com/ai-dev-control-plane/runtimes

go 1.25.11

require (
	github.com/go-chi/chi/v5 v5.3.0
	golang.org/x/sys v0.46.0
)
