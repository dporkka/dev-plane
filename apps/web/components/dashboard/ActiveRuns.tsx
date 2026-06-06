'use client';

import React from 'react';
import Link from 'next/link';
import type { AgentRun } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { CostBadge } from '@/components/run/CostBadge';
import { SkeletonCard } from '@/components/ui/skeleton';
import {
  Activity,
  Loader2,
  Bot,
  Cpu,
  Zap,
} from 'lucide-react';

interface ActiveRunsProps {
  runs: AgentRun[];
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

export function ActiveRuns({ runs, isLoading }: ActiveRunsProps) {
  if (isLoading) {
    return (
      <div>
        <h2 className="text-lg font-semibold text-white mb-3">Active Runs</h2>
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    );
  }

  const activeRuns = runs.filter(
    (r) => r.status === 'running' || r.status === 'pending'
  );

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
          return (
            <Card key={run.id} className="py-3">
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-3 min-w-0">
                  <div className="p-1.5 rounded bg-[#21262d] flex-shrink-0">
                    <Icon className="w-4 h-4 text-gray-400" />
                  </div>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-gray-200 capitalize truncate">
                        {run.agent_role.replace('_', ' ')}
                      </span>
                      <StatusBadge status={run.status} size="sm" />
                    </div>
                    {run.model && (
                      <span className="text-xs text-gray-600">{run.model}</span>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-3 flex-shrink-0">
                  <CostBadge cost={run.total_cost} />
                  {run.status === 'running' && (
                    <Loader2 className="w-4 h-4 text-blue-400 animate-spin" />
                  )}
                </div>
              </div>
            </Card>
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
