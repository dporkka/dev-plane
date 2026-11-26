# Dependency-aware execution scheduler

Dev Plane's existing scheduler validates task dependency DAGs, ownership paths,
CPU/memory capacity, and active claims. No external task database is required.

## Optional unblock-first ordering

Set `Manifest.PrioritizeUnblocking = true` or
`WorkScheduleOptions.PrioritizeUnblocking = true` when choosing among runnable
tasks. The scheduler ranks a candidate by the number of **distinct pending
transitive dependents** it can help unlock, then keeps the caller's original
task order for ties.

This is an ordering hint, not a change to the safety rules:

- Dependencies still must succeed before their dependents run.
- Active work consumes capacity and retains its ownership claims.
- CPU/memory limits and conflicting ownership paths remain binding.
- Terminal dependents do not contribute to the unblock score.
- A failed or skipped prerequisite causes **all pending downstream work** to
  receive a `dependency-failed` skip in the same scheduler decision.
- The option defaults to **false**, preserving existing scheduling order.
- An agent may not use this score to authorize a PR merge or deployment.

Example (Go):

```go
decision, err := scheduler.NextWork(items, scheduler.WorkScheduleOptions{
    MaxParallelCost:      3,
    Now:                  time.Now().UTC(),
    PrioritizeUnblocking: true,
})
```

Use this policy for experimental dependency-aware scheduling, not as a
replacement for customer impact, explicit priority, or risk review. Measure
whether it actually reduces blocked-task age and accepted change latency.
The production run-admission code checks individual candidate runs and does
not yet use this option for portfolio-wide prioritization.
