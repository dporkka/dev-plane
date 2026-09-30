import type {
  ReadinessCheck,
  ReadinessReport,
  ReadinessStatus,
} from "./types";

export interface AdmissionEvidence {
  policy: string;
  readiness: ReadinessReport;
  trigger?: string;
  retryOriginalRunId?: string;
  handoffFromRunId?: string;
  handoffFromAgent?: string;
}

const readinessStatuses = new Set<ReadinessStatus>([
  "ready",
  "attention",
  "blocked",
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isReadinessStatus(value: unknown): value is ReadinessStatus {
  return typeof value === "string" && readinessStatuses.has(value as ReadinessStatus);
}

function parseCheck(value: unknown): ReadinessCheck | null {
  if (!isRecord(value)) return null;
  if (
    typeof value.id !== "string" ||
    typeof value.title !== "string" ||
    !isReadinessStatus(value.status) ||
    typeof value.critical !== "boolean"
  ) {
    return null;
  }

  const evidence = Array.isArray(value.evidence)
    ? value.evidence.filter((item): item is string => typeof item === "string")
    : undefined;

  return {
    id: value.id,
    title: value.title,
    status: value.status,
    critical: value.critical,
    ...(evidence && evidence.length > 0 ? { evidence } : {}),
    ...(typeof value.recommendation === "string"
      ? { recommendation: value.recommendation }
      : {}),
  };
}

function parseReadinessReport(value: unknown): ReadinessReport | null {
  if (!isRecord(value) || !isReadinessStatus(value.status) || !Array.isArray(value.checks)) {
    return null;
  }

  const checks: ReadinessCheck[] = [];
  for (const item of value.checks) {
    const check = parseCheck(item);
    if (!check) return null;
    checks.push(check);
  }

  return { status: value.status, checks };
}

export function getAdmissionEvidence(metadata: unknown): AdmissionEvidence | null {
  if (!isRecord(metadata) || !isRecord(metadata.admission)) return null;

  const policy = metadata.admission.policy;
  const readiness = parseReadinessReport(metadata.admission.readiness);
  if (typeof policy !== "string" || !readiness) return null;

  const retry = isRecord(metadata.retry) ? metadata.retry : undefined;

  return {
    policy,
    readiness,
    ...(typeof metadata.trigger === "string" ? { trigger: metadata.trigger } : {}),
    ...(typeof retry?.original_run_id === "string"
      ? { retryOriginalRunId: retry.original_run_id }
      : {}),
    ...(typeof metadata.handoff_from_run_id === "string"
      ? { handoffFromRunId: metadata.handoff_from_run_id }
      : {}),
    ...(typeof metadata.handoff_from_agent === "string"
      ? { handoffFromAgent: metadata.handoff_from_agent }
      : {}),
  };
}
