"use client";

import type { TaskSchedulingDiagnostics as SchedulingSnapshot } from "@ai-cp/dev-plane-sdk";

const checkLabels: Record<string, string> = {
  dependencies: "Dependencies",
  ownership: "File ownership conflicts",
  capacity: "Worker capacity",
  budget: "Execution budget",
};

const descriptions: Record<string, string> = {
  "no-dependencies-configured": "No scheduler prerequisites configured",
  "all-prerequisites-done": "All checked prerequisites are done",
  "dependency-not-done": "A prerequisite is not done",
  "dependency-unavailable": "A prerequisite cannot be verified in this project",
  "invalid-dependency": "Invalid dependency reference",
  "invalid-scheduler-metadata": "Invalid scheduler metadata",
  "no-observed-conflicts": "No conflicts among checked active claims",
  "ownership-conflict": "An active run owns an overlapping path",
  "ownership-not-configured": "No ownership specification is available",
  "running-ownership-unavailable":
    "An active run's ownership cannot be checked",
  "claim-snapshot-truncated": "Too many active claims to inspect safely",
  "worker-capacity-not-exposed":
    "Worker capacity configuration is unavailable to the API",
  "worker-budget-not-exposed": "Worker budget policy cannot be checked here",
  "not-evaluated": "Not evaluated",
};

interface TaskSchedulingPanelProps {
  snapshot?: SchedulingSnapshot;
  loading: boolean;
  error: boolean;
  refreshing: boolean;
  onRefresh: () => void;
}

export function TaskSchedulingPanel({
  snapshot,
  loading,
  error,
  refreshing,
  onRefresh,
}: TaskSchedulingPanelProps) {
  return (
    <section aria-label="Scheduling diagnostics" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold text-white">
          Scheduling diagnostics
        </h2>
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading || refreshing}
          className="btn-secondary text-sm"
        >
          {refreshing ? "Refreshing…" : "Refresh snapshot"}
        </button>
      </div>
      {loading ? (
        <output className="block text-sm text-gray-400">
          Loading scheduling snapshot…
        </output>
      ) : error || !snapshot ? (
        <p className="text-sm text-amber-300">
          Scheduling diagnostics are unavailable. This does not mean a run is
          ready.
        </p>
      ) : (
        <div className="rounded-md border border-[#30363d] bg-[#161b22] p-4">
          <p className="mb-3 text-sm text-gray-300">
            {snapshot.status === "blocked"
              ? "One or more observed scheduling blockers"
              : "No verified blocker in the available checks; admission is still unknown"}
          </p>
          <dl className="space-y-2">
            {Object.entries(checkLabels).map(([key, label]) => {
              const check = snapshot.checks[key];
              return (
                <div
                  key={key}
                  className="flex flex-wrap justify-between gap-2 text-sm"
                >
                  <dt className="text-gray-300">{label}</dt>
                  <dd className="text-right text-gray-400">
                    <span
                      className={
                        check?.status === "blocked"
                          ? "text-red-300"
                          : check?.status === "clear"
                            ? "text-green-300"
                            : "text-amber-300"
                      }
                    >
                      {check?.status ?? "unknown"}
                    </span>
                    {" · "}
                    {check
                      ? (descriptions[check.reason] ?? "Not verified")
                      : "Not verified"}
                  </dd>
                </div>
              );
            })}
          </dl>
          <p className="mt-3 text-xs text-gray-500">
            {snapshot.active_claims} observed active claims · Checked{" "}
            {new Date(snapshot.observed_at).toLocaleString()}
          </p>
        </div>
      )}
      <p className="text-xs text-gray-500">
        Read-only database snapshot, not a live admission decision. The worker
        remains authoritative for capacity, budgets, approvals, and atomic run
        claims. Unknown does not mean safe to execute.
      </p>
    </section>
  );
}
