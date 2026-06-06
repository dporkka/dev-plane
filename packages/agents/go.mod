// Package agents defines the agent framework interfaces, tool system, and role configurations.
//
// An Agent is a typed worker (planner, implementer, reviewer, etc.) that executes
// tasks within a workspace using a defined set of tools. This package provides the
// contracts that concrete agent implementations must satisfy.
module github.com/ai-dev-control-plane/agents

go 1.23

require github.com/ai-dev-control-plane/models v0.0.0

replace github.com/ai-dev-control-plane/models => ../models
