// Package runtimes defines the Provider interface for workspace runtimes and
// provides Docker and local implementations.
//
// A runtime Provider manages the lifecycle of isolated workspaces where agents
// execute commands, read/write files, and apply code patches.
module github.com/ai-dev-control-plane/runtimes

go 1.23
