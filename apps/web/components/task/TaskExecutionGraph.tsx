"use client";

import { StatusBadge } from "@/components/common/StatusBadge";
import { CostBadge } from "@/components/run/CostBadge";
import type { AgentRun, RunExecutionEvidence } from "@/lib/types";
import { buildExecutionGraph } from "@/lib/execution-graph";
import {
  AlertTriangle,
  Bot,
  CheckCircle,
  CircleDollarSign,
  FileArchive,
  FlaskConical,
  GitCommit,
  GitPullRequest,
  RotateCcw,
  Waypoints,
  XCircle,
  Zap,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { useMemo } from "react";
import ReactFlow, {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  type Edge,
  type Node,
} from "reactflow";
import "reactflow/dist/style.css";

interface ExecutionRunNodeData {
  run: AgentRun;
  orphaned: boolean;
  evidence?: RunExecutionEvidence;
}

const outcomeClasses: Record<string, string> = {
  passed: "border-green-500/30 bg-green-500/10 text-green-300",
  failed: "border-orange-500/30 bg-orange-500/10 text-orange-300",
  error: "border-red-500/30 bg-red-500/10 text-red-300",
  cancelled: "border-gray-500/30 bg-gray-500/10 text-gray-300",
  skipped: "border-gray-600/30 bg-gray-600/10 text-gray-400",
};

function runTriggerLabel(run: AgentRun): string | null {
  const trigger = run.metadata?.trigger;
  if (trigger === "review_repair") {
    const round = run.metadata?.repair_round;
    return typeof round === "number" ? `repair #${round}` : "repair";
  }
  if (trigger === "mailbox_handoff") return "handoff";
  if (trigger === "retry") return "retry";
  return null;
}

function ExecutionRunNode({ data }: { data: ExecutionRunNodeData }) {
  const { run, orphaned, evidence } = data;
  const trigger = runTriggerLabel(run);
  const RoleIcon = run.agent_role === "implementer" ? Zap : Bot;

  return (
    <div className="min-w-[250px] rounded-lg border border-[#30363d] bg-[#161b22] p-3 shadow-lg">
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2 !w-2 !border-[#30363d] !bg-gray-500"
      />
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <div className="rounded-md bg-blue-500/10 p-1.5 text-blue-400">
            <RoleIcon className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium capitalize text-gray-100">
              {run.agent_role.replace("_", " ")}
            </div>
            <div className="text-[11px] text-gray-500">attempt {run.attempt || 1}</div>
          </div>
        </div>
        <StatusBadge status={run.status} size="sm" />
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        {run.outcome && (
          <span
            className={`rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${
              outcomeClasses[run.outcome] || outcomeClasses.skipped
            }`}
          >
            {run.outcome}
          </span>
        )}
        {trigger && (
          <span className="rounded border border-violet-500/25 bg-violet-500/10 px-1.5 py-0.5 text-[10px] text-violet-300">
            {trigger}
          </span>
        )}
        {orphaned && (
          <span className="flex items-center gap-1 rounded border border-yellow-500/25 bg-yellow-500/10 px-1.5 py-0.5 text-[10px] text-yellow-300">
            <AlertTriangle className="h-3 w-3" />
            orphan
          </span>
        )}
      </div>

      {evidence && (
        <div className="mt-3 space-y-1.5 border-t border-[#30363d] pt-2 text-[10px]">
          <div className="flex flex-wrap gap-1.5">
            {evidence.commit_hash && (
              <EvidenceBadge
                icon={GitCommit}
                label={evidence.commit_hash.slice(0, 8)}
                title={`commit ${evidence.commit_hash}`}
              />
            )}
            {evidence.verification && (
              <EvidenceBadge
                icon={FlaskConical}
                label={
                  evidence.verification.passed
                    ? `tests ✓${evidence.verification.total ? ` · ${evidence.verification.total}` : ""}`
                    : `tests ✕${evidence.verification.failed ? ` · ${evidence.verification.failed} failed` : ""}`
                }
                tone={evidence.verification.passed ? "success" : "danger"}
                title={`verification step ${evidence.verification.step_id}`}
              />
            )}
            {evidence.review && (
              <EvidenceBadge
                icon={CheckCircle}
                label={
                  evidence.review.approvable
                    ? `review ✓ · ${evidence.review.diff_summary.files_changed || 0} files`
                    : `review changes · ${evidence.review.diff_summary.files_changed || 0} files`
                }
                tone={evidence.review.approvable ? "success" : "warning"}
                title={evidence.review.summary}
              />
            )}
            {evidence.pull_request && (
              <EvidenceBadge
                icon={GitPullRequest}
                label={`PR #${evidence.pull_request.number} · ${evidence.pull_request.state}`}
                tone="info"
                title={evidence.pull_request.title}
              />
            )}
            {evidence.artifacts.length > 0 && (
              <EvidenceBadge
                icon={FileArchive}
                label={`${evidence.artifacts.length} artifact${evidence.artifacts.length === 1 ? "" : "s"}`}
                title={evidence.artifacts.map((artifact) => artifact.file_name).join(", ")}
              />
            )}
          </div>
          {evidence.review?.diff_summary && (
            <div className="text-gray-500">
              diff {evidence.review.diff_summary.insertions >= 0 ? "+" : ""}
              {evidence.review.diff_summary.insertions} / -
              {Math.abs(evidence.review.diff_summary.deletions)}
            </div>
          )}
          {evidence.failure && (
            <div
              className="truncate rounded border border-red-500/20 bg-red-500/5 px-2 py-1 text-red-300"
              title={evidence.failure.message}
            >
              {evidence.failure.message}
            </div>
          )}
        </div>
      )}

      <div className="mt-3 flex items-center justify-between gap-3 border-t border-[#30363d] pt-2 text-[11px] text-gray-500">
        <span className="truncate">{run.model || run.execution_snapshot?.model_route || "model pending"}</span>
        <CostBadge cost={run.total_cost || 0} />
      </div>

      <Handle
        type="source"
        position={Position.Right}
        className="!h-2 !w-2 !border-[#30363d] !bg-blue-500"
      />
    </div>
  );
}

const nodeTypes = {
  executionRun: ExecutionRunNode,
};

export function TaskExecutionGraph({
  runs,
  evidence = {},
}: {
  runs: AgentRun[];
  evidence?: Record<string, RunExecutionEvidence>;
}) {
  const router = useRouter();
  const graph = useMemo(() => buildExecutionGraph(runs), [runs]);

  const nodes: Node<ExecutionRunNodeData>[] = useMemo(
    () =>
      graph.nodes.map((node) => ({
        id: node.id,
        type: "executionRun",
        position: node.position,
        data: {
          run: node.run,
          orphaned: node.orphaned,
          evidence: evidence[node.id],
        },
      })),
    [graph.nodes, evidence],
  );

  const edges: Edge[] = useMemo(
    () =>
      graph.edges.map((edge) => ({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        label: edge.label,
        type: "smoothstep",
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 14,
          height: 14,
          color: edge.label.startsWith("repair") ? "#f59e0b" : "#6b7280",
        },
        style: {
          stroke: edge.label.startsWith("repair") ? "#f59e0b" : "#6b7280",
          strokeWidth: edge.label.startsWith("repair") ? 2 : 1.5,
          strokeDasharray: edge.label.startsWith("repair") ? "5 4" : undefined,
        },
        labelStyle: {
          fill: "#9ca3af",
          fontSize: 10,
        },
        labelBgStyle: {
          fill: "#0d1117",
          fillOpacity: 0.9,
        },
      })),
    [graph.edges],
  );

  if (runs.length === 0) {
    return (
      <div className="rounded-lg border border-[#30363d] bg-[#0d1117] py-12 text-center text-sm text-gray-500">
        No runs yet. Start the task to begin execution.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-2 md:grid-cols-4 lg:grid-cols-7">
        <SummaryStat icon={Waypoints} label="Runs" value={graph.summary.total} />
        <SummaryStat icon={Zap} label="Active" value={graph.summary.active} />
        <SummaryStat icon={CheckCircle} label="Passed" value={graph.summary.passed} />
        <SummaryStat icon={XCircle} label="Failed" value={graph.summary.failed} />
        <SummaryStat icon={AlertTriangle} label="Errors" value={graph.summary.errors} />
        <SummaryStat icon={RotateCcw} label="Repairs" value={graph.summary.repairs} />
        <SummaryStat
          icon={CircleDollarSign}
          label="Cost"
          value={`$${graph.summary.totalCost.toFixed(2)}`}
        />
      </div>

      <div className="h-[460px] overflow-hidden rounded-lg border border-[#30363d] bg-[#0d1117]">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
          fitView
          fitViewOptions={{ padding: 0.18, maxZoom: 1.1 }}
          minZoom={0.35}
          maxZoom={1.4}
          proOptions={{ hideAttribution: true }}
          onNodeClick={(_, node) => router.push(`/runs/${node.id}`)}
        >
          <Background color="#21262d" gap={20} size={1} />
          <Controls
            showInteractive={false}
            className="border-gray-700 bg-gray-900 text-gray-300"
          />
        </ReactFlow>
      </div>
    </div>
  );
}

function EvidenceBadge({
  icon: Icon,
  label,
  title,
  tone = "neutral",
}: {
  icon: typeof Waypoints;
  label: string;
  title?: string;
  tone?: "neutral" | "success" | "warning" | "danger" | "info";
}) {
  const tones = {
    neutral: "border-gray-600/30 bg-gray-600/10 text-gray-300",
    success: "border-green-500/30 bg-green-500/10 text-green-300",
    warning: "border-orange-500/30 bg-orange-500/10 text-orange-300",
    danger: "border-red-500/30 bg-red-500/10 text-red-300",
    info: "border-blue-500/30 bg-blue-500/10 text-blue-300",
  };

  return (
    <span
      className={`flex items-center gap-1 rounded border px-1.5 py-0.5 ${tones[tone]}`}
      title={title}
    >
      <Icon className="h-3 w-3" />
      {label}
    </span>
  );
}

function SummaryStat({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Waypoints;
  label: string;
  value: number | string;
}) {
  return (
    <div className="rounded-lg border border-[#30363d] bg-[#161b22] px-3 py-2">
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-gray-500">
        <Icon className="h-3 w-3" />
        {label}
      </div>
      <div className="mt-1 text-sm font-semibold text-gray-100">{value}</div>
    </div>
  );
}
