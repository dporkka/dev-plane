'use client';

import React from 'react';
import Link from 'next/link';
import type { AgentRun } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { SkeletonCard } from '@/components/ui/skeleton';
import { StatusBadge } from '@/components/common/StatusBadge';
import {
  Activity,
  Loader2,
  Bot,
  Cpu,
  Zap,
  Clock,
  DollarSign,
} from 'lucide-react';

interface ActiveRun {
  id: string;
  task_name: string;
  repository?: string;
  agent_role: string;
  model?: string;
  status: string;
  current_step?: string;
  progress?: number;
  elapsed_seconds?: number;
  cost?: number;
}

interface ActiveRunsWidgetProps {
  runs: ActiveRun[];
  isLoading?: boolean;
}

const roleIcons: Record<string, React.ElementType> = {
  planner: Cpu,
  implementer: Zap,
  reviewer: Bot,
  test_runner: Zap,
  security_reviewer: Bot,
  docs_writer: Bot,
  release_manager: Zap,
};

function formatElapsed(seconds: number): string {
  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${mins}:${secs.toString().padStart(2, '0')}`;
}

export function ActiveRunsWidget({ runs, isLoading }: ActiveRunsWidgetProps) {
  if (isLoading) {
    return (
      <div>
        <h2 className="text-lg font-semibold text-white mb-3 flex items-center gap-2">
          <Activity className="w-5 h-5 text-blue-400" />
          Active Runs
        </h2>
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    );
  }

  const activeRuns = runs.filter((r) => r.status === 'running' || r.status === 'pending');

  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        <Activity className="w-5 h-5 text-blue-400" />
        <h2 className="text-lg font-semibold text-white">Active Runs</h2>
        {activeRuns.length > 0 && (
          <span className="text-xs bg-blue-500/10 text-blue-400 px-2 py-0.5 rounded-full">
            {activeRuns.length}
          </span>
        )}
      </div>
      <div className="space-y-2">
        {runs.slice(0, 6).map((run) => {
          const Icon = roleIcons[run.agent_role] || Bot;
          const progress = run.progress || 0;
          const isRunning = run.status === 'running';

          return (
            <Link href={`/runs/${run.id}`} key={run.id}>
              <Card className="py-3 hover:bg-[#161b22] transition-colors cursor-pointer">
                <div className="flex items-center justify-between gap-3 mb-2">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="p-1.5 rounded bg-[#21262d] flex-shrink-0">
                      <Icon className="w-4 h-4 text-gray-400" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm text-gray-200 capitalize truncate">
                          {run.task_name || run.agent_role.replace('_', ' ')}
                        </span>
                        <StatusBadge status={run.status} size="sm" />
                      </div>
                      {run.repository && (
                        <span className="text-xs text-gray-600">{run.repository}</span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-3 flex-shrink-0">
                    {run.cost !== undefined && (
                      <span className="text-xs text-gray-500 flex items-center gap-1">
                        <DollarSign className="w-3 h-3" />
                        {run.cost.toFixed(4)}
                      </span>
                    )}
                    {isRunning && (
                      <Loader2 className="w-4 h-4 text-blue-400 animate-spin" />
                    )}
                  </div>
                </div>

                {/* Progress bar */}
                {isRunning && (
                  <div className="space-y-1">
                    <div className="flex items-center justify-between text-xs text-gray-500">
                      <span className="truncate max-w-[60%]">{run.current_step || 'Running...'}</span>
                      <div className="flex items-center gap-2">
                        {run.elapsed_seconds !== undefined && (
                          <span className="flex items-center gap-1">
                            <Clock className="w-3 h-3" />
                            {formatElapsed(run.elapsed_seconds)}
                          </span>
                        )}
                        <span>{Math.round(progress)}%</span>
                      </div>
                    </div>
                    <div className="h-1 bg-[#21262d] rounded-full overflow-hidden">
                      <div
                        className="h-full bg-blue-500 rounded-full transition-all duration-500"
                        style={{ width: `${Math.min(progress, 100)}%` }}
                      />
                    </div>
                  </div>
                )}
              </Card>
            </Link>
          );
        })}
        {runs.length === 0 && (
          <div className="text-center py-8 text-gray-500 text-sm">
            No active runs
          </div>
        )}
      </div>
    </div>
  );
}
