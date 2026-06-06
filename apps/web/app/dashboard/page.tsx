'use client';

import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import { StatsCards } from '@/components/dashboard/StatsCards';
import { RecentTasks } from '@/components/dashboard/RecentTasks';
import { ActiveRunsWidget } from '@/components/dashboard/ActiveRunsWidget';
import { PendingApprovalsWidget } from '@/components/dashboard/PendingApprovalsWidget';
import { Card } from '@/components/ui/card';
import {
  AlertTriangle,
  GitPullRequest,
  TrendingUp,
} from 'lucide-react';
import Link from 'next/link';

export default function DashboardPage() {
  const { selectedOrg } = useStore();

  const { data: dashboard, isLoading } = useQuery({
    queryKey: ['dashboard', selectedOrg],
    queryFn: () =>
      selectedOrg ? api.getDashboard(selectedOrg) : Promise.resolve(null),
    enabled: !!selectedOrg,
  });

  const { data: approvalsData } = useQuery({
    queryKey: ['approvals-pending', selectedOrg],
    queryFn: () =>
      selectedOrg ? api.listApprovals(selectedOrg) : Promise.resolve([]),
    enabled: !!selectedOrg,
  });

  // Mock cost data - in real app, would come from API
  const costData = [
    { day: 'Mon', cost: 0.45 },
    { day: 'Tue', cost: 1.23 },
    { day: 'Wed', cost: 0.89 },
    { day: 'Thu', cost: 2.15 },
    { day: 'Fri', cost: 1.67 },
    { day: 'Sat', cost: 0.34 },
    { day: 'Sun', cost: 0.12 },
  ];

  const maxCost = Math.max(...costData.map((d) => d.cost));

  const approvalList = (approvalsData as any)?.data || approvalsData || [];

  // Mock active runs with enhanced data
  const activeRuns = (dashboard?.active_runs || []).map((run: any) => ({
    id: run.id,
    task_name: run.task_id?.slice(0, 8),
    agent_role: run.agent_role,
    model: run.model,
    status: run.status,
    current_step: run.status === 'running' ? 'Executing...' : undefined,
    progress: run.status === 'running' ? 65 : undefined,
    elapsed_seconds: run.started_at
      ? Math.floor((Date.now() - new Date(run.started_at).getTime()) / 1000)
      : undefined,
    cost: run.total_cost,
  }));

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white">Dashboard</h1>
        <p className="text-gray-500 mt-1">Overview of your AI development pipeline</p>
      </div>

      <StatsCards data={dashboard?.stats} isLoading={isLoading} />

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <ActiveRunsWidget runs={activeRuns} isLoading={isLoading} />
        <PendingApprovalsWidget approvals={approvalList} isLoading={isLoading} />
      </div>

      {/* Cost chart */}
      <div>
        <h2 className="text-lg font-semibold text-white mb-3 flex items-center gap-2">
          <TrendingUp className="w-5 h-5 text-green-400" />
          Daily Cost
        </h2>
        <Card className="p-4">
          <div className="flex items-end gap-2 h-32">
            {costData.map((d) => (
              <div key={d.day} className="flex-1 flex flex-col items-center gap-1">
                <div className="w-full flex justify-center">
                  <div
                    className="w-full max-w-[40px] bg-blue-500/60 hover:bg-blue-500 transition-colors rounded-t-sm"
                    style={{ height: `${(d.cost / maxCost) * 100}px` }}
                    title={`${d.day}: $${d.cost.toFixed(2)}`}
                  />
                </div>
                <span className="text-[10px] text-gray-500">{d.day}</span>
              </div>
            ))}
          </div>
          <div className="flex items-center justify-between mt-2 text-xs text-gray-500">
            <span>Total this week: ${costData.reduce((a, b) => a + b.cost, 0).toFixed(2)}</span>
            <span>Avg: ${(costData.reduce((a, b) => a + b.cost, 0) / costData.length).toFixed(2)}/day</span>
          </div>
        </Card>
      </div>

      {/* Failed runs alert */}
      {dashboard?.stats?.failed_runs && (dashboard.stats.failed_runs as number) > 0 && (
        <Card className="p-4 bg-red-500/5 border-red-500/30">
          <div className="flex items-center gap-3">
            <AlertTriangle className="w-5 h-5 text-red-400" />
            <div>
              <div className="text-sm font-medium text-red-400">
                {(dashboard.stats.failed_runs as number)} run(s) failed recently
              </div>
              <div className="text-xs text-gray-500">
                Check the runs page for details and retry options.
              </div>
            </div>
            <Link href="/tasks" className="btn-secondary text-xs ml-auto">
              View Tasks
            </Link>
          </div>
        </Card>
      )}

      {/* Recent PRs */}
      <div>
        <h2 className="text-lg font-semibold text-white mb-3 flex items-center gap-2">
          <GitPullRequest className="w-5 h-5 text-purple-400" />
          Recent Activity
        </h2>
        <RecentTasks tasks={dashboard?.recent_tasks || []} isLoading={isLoading} />
      </div>
    </div>
  );
}
