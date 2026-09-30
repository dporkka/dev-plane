import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import type {
  ReadinessCheck,
  ReadinessReport,
  ReadinessStatus,
} from "@/lib/types";
import {
  AlertTriangle,
  CheckCircle2,
  ShieldCheck,
  XCircle,
} from "lucide-react";

interface EvidenceContext {
  label: string;
  value: string;
}

interface ReadinessEvidenceCardProps {
  report: ReadinessReport;
  title?: string;
  policy?: string;
  context?: EvidenceContext[];
}

const statusConfig: Record<
  ReadinessStatus,
  {
    label: string;
    variant: "success" | "warning" | "danger";
    icon: typeof CheckCircle2;
    textClass: string;
  }
> = {
  ready: {
    label: "Ready",
    variant: "success",
    icon: CheckCircle2,
    textClass: "text-green-400",
  },
  attention: {
    label: "Attention",
    variant: "warning",
    icon: AlertTriangle,
    textClass: "text-yellow-400",
  },
  blocked: {
    label: "Blocked",
    variant: "danger",
    icon: XCircle,
    textClass: "text-red-400",
  },
};

function CheckRow({ check }: { check: ReadinessCheck }) {
  const config = statusConfig[check.status];
  const Icon = config.icon;

  return (
    <div className="rounded-md border border-[#30363d] bg-[#0d1117]/60 p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2 min-w-0">
          <Icon className={`h-4 w-4 shrink-0 ${config.textClass}`} />
          <span className="text-sm font-medium text-gray-200">
            {check.title}
          </span>
          {check.critical && (
            <Badge variant="outline" className="text-[10px]">
              Critical
            </Badge>
          )}
        </div>
        <Badge variant={config.variant}>{config.label}</Badge>
      </div>

      {check.evidence && check.evidence.length > 0 && (
        <div className="mt-2 space-y-1">
          {check.evidence.map((item) => (
            <div
              key={item}
              className="truncate font-mono text-xs text-gray-500"
              title={item}
            >
              {item}
            </div>
          ))}
        </div>
      )}

      {check.recommendation && (
        <p className="mt-2 text-xs leading-5 text-gray-400">
          {check.recommendation}
        </p>
      )}
    </div>
  );
}

export function ReadinessEvidenceCard({
  report,
  title = "Readiness",
  policy,
  context = [],
}: ReadinessEvidenceCardProps) {
  const config = statusConfig[report.status];
  const Icon = config.icon;

  return (
    <Card>
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5 text-blue-400" />
            <h2 className="text-base font-semibold text-white">{title}</h2>
          </div>
          <p className="mt-1 text-xs text-gray-500">
            Deterministic evidence used to decide whether autonomous work can
            proceed.
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          {policy && <Badge variant="outline">{policy}</Badge>}
          <Badge variant={config.variant} className="gap-1">
            <Icon className="h-3 w-3" />
            {config.label}
          </Badge>
        </div>
      </div>

      {context.length > 0 && (
        <div className="mt-4 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {context.map((item) => (
            <div
              key={`${item.label}:${item.value}`}
              className="rounded-md border border-[#30363d] bg-[#0d1117]/60 px-3 py-2"
            >
              <div className="text-[11px] uppercase tracking-wide text-gray-600">
                {item.label}
              </div>
              <div className="mt-1 truncate font-mono text-xs text-gray-300">
                {item.value}
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="mt-4 space-y-2">
        {report.checks.map((check) => (
          <CheckRow key={check.id} check={check} />
        ))}
        {report.checks.length === 0 && (
          <div className="text-sm text-gray-500">
            No readiness checks were recorded.
          </div>
        )}
      </div>
    </Card>
  );
}
