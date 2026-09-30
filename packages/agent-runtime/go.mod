// Package agentruntime defines provider-neutral coding-agent runtime contracts.
//
// It separates agent conversation/execution capabilities from workspace runtime
// providers so Codex, Claude Code, OpenCode, and other coding agents can be
// orchestrated through one stable control-plane protocol.
module github.com/ai-dev-control-plane/agent-runtime

go 1.25.11
