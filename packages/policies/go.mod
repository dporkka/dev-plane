// Package policies provides a policy engine for evaluating security and governance
// rules against actions taken in the system.
//
// The engine supports role-based access control, resource-specific policies,
// and configurable effects (allow, ask, deny, admin_only).
module github.com/ai-dev-control-plane/policies

go 1.23

require github.com/ai-dev-control-plane/models v0.0.0

replace github.com/ai-dev-control-plane/models => ../models
