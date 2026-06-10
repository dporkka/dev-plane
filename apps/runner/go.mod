// Runner (sandbox/runtime) service for the AI Dev Control Plane.
//
// The runner owns workspace runtime provisioning: it initializes the
// configured runtime Provider (local worktree or Docker container) where
// agents create workspaces, execute commands, read/write files, and apply
// patches.
module github.com/ai-dev-control-plane/runner

go 1.25.0

require github.com/ai-dev-control-plane/runtimes v0.0.0

replace github.com/ai-dev-control-plane/runtimes => ../../packages/runtimes
