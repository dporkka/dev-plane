import type { AgentRun } from "./types";

export interface ExecutionGraphNode {
  id: string;
  run: AgentRun;
  depth: number;
  orphaned: boolean;
  position: { x: number; y: number };
}

export interface ExecutionGraphEdge {
  id: string;
  source: string;
  target: string;
  label: string;
}

export interface ExecutionGraphSummary {
  total: number;
  active: number;
  passed: number;
  failed: number;
  errors: number;
  cancelled: number;
  repairs: number;
  totalCost: number;
}

export interface ExecutionGraph {
  nodes: ExecutionGraphNode[];
  edges: ExecutionGraphEdge[];
  summary: ExecutionGraphSummary;
}

function createdAt(run: AgentRun): number {
  const value = Date.parse(run.created_at);
  return Number.isFinite(value) ? value : 0;
}

function compareRuns(a: AgentRun, b: AgentRun): number {
  const timeDiff = createdAt(a) - createdAt(b);
  if (timeDiff !== 0) return timeDiff;
  const attemptDiff = (a.attempt || 1) - (b.attempt || 1);
  if (attemptDiff !== 0) return attemptDiff;
  return a.id.localeCompare(b.id);
}

function metadataNumber(run: AgentRun, key: string): number | undefined {
  const value = run.metadata?.[key];
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

export function runRelationLabel(run: AgentRun): string {
  const trigger = run.metadata?.trigger;
  if (trigger === "review_repair") {
    const round = metadataNumber(run, "repair_round");
    return round && round > 0 ? `repair #${round}` : "repair";
  }
  if (trigger === "mailbox_handoff") return "handoff";
  if (trigger === "retry") return "retry";
  if ((run.attempt || 1) > 1) return `attempt ${run.attempt}`;
  return "child";
}

export function buildExecutionGraph(runs: AgentRun[]): ExecutionGraph {
  const sorted = [...runs].sort(compareRuns);
  const byID = new Map(sorted.map((run) => [run.id, run]));
  const depths = new Map<string, number>();
  const visiting = new Set<string>();

  const depthFor = (run: AgentRun): number => {
    const existing = depths.get(run.id);
    if (existing !== undefined) return existing;
    if (visiting.has(run.id)) return 0;

    visiting.add(run.id);
    let depth = 0;
    if (run.parent_run_id) {
      const parent = byID.get(run.parent_run_id);
      if (parent && parent.id !== run.id) {
        depth = depthFor(parent) + 1;
      }
    }
    visiting.delete(run.id);
    depths.set(run.id, depth);
    return depth;
  };

  for (const run of sorted) depthFor(run);

  const rowsByDepth = new Map<number, AgentRun[]>();
  for (const run of sorted) {
    const depth = depths.get(run.id) ?? 0;
    const row = rowsByDepth.get(depth) ?? [];
    row.push(run);
    rowsByDepth.set(depth, row);
  }

  const rowIndex = new Map<string, number>();
  for (const row of rowsByDepth.values()) {
    row.sort(compareRuns).forEach((run, index) => rowIndex.set(run.id, index));
  }

  const nodes = sorted
    .map((run) => {
      const depth = depths.get(run.id) ?? 0;
      return {
        id: run.id,
        run,
        depth,
        orphaned: Boolean(run.parent_run_id && !byID.has(run.parent_run_id)),
        position: {
          x: depth * 360,
          y: (rowIndex.get(run.id) ?? 0) * 250,
        },
      };
    })
    .sort((a, b) => a.depth - b.depth || compareRuns(a.run, b.run));

  const edges = sorted
    .filter(
      (run) =>
        Boolean(run.parent_run_id) &&
        run.parent_run_id !== run.id &&
        byID.has(run.parent_run_id as string),
    )
    .map((run) => ({
      id: `run-${run.parent_run_id}-${run.id}`,
      source: run.parent_run_id as string,
      target: run.id,
      label: runRelationLabel(run),
    }));

  const summary: ExecutionGraphSummary = {
    total: sorted.length,
    active: sorted.filter((run) =>
      ["pending", "queued", "running", "paused"].includes(run.status),
    ).length,
    passed: sorted.filter((run) => run.outcome === "passed").length,
    failed: sorted.filter((run) => run.outcome === "failed").length,
    errors: sorted.filter((run) => run.outcome === "error").length,
    cancelled: sorted.filter((run) => run.outcome === "cancelled").length,
    repairs: sorted.filter((run) => run.metadata?.trigger === "review_repair").length,
    totalCost: sorted.reduce((total, run) => total + (run.total_cost || 0), 0),
  };

  return { nodes, edges, summary };
}
