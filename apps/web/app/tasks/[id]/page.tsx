'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Loading } from '@/components/common/Loading';
import { StatusBadge } from '@/components/common/StatusBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import { CostBadge } from '@/components/run/CostBadge';
import { TaskSpecPreview } from '@/components/task/TaskSpecPreview';
import { TaskActions } from '@/components/task/TaskActions';
import type { TaskSpec } from '@/lib/types';
import {
  ArrowLeft,
  GitCommit,
  Clock,
  DollarSign,
  Layers,
  AlertTriangle,
  FolderGit,
  CheckCircle,
  XCircle,
  GitPullRequest,
  ExternalLink,
} from 'lucide-react';

function parseSpec(task: any): TaskSpec | null {
  if (!task?.spec) return null;
  if (typeof task.spec === 'object' && task.spec.summary) {
    return {
      id: task.spec.id || `spec-${task.id}`,
      task_id: task.id,
      summary: task.spec.summary || task.title,
      problem_statement: task.spec.problem_statement || task.spec.problem || task.description || '',
      implementation_plan: task.spec.implementation_plan || task.spec.plan || [],
      files_to_change: task.spec.files_to_change || [],
      files_to_create: task.spec.files_to_create || [],
      acceptance_criteria: task.spec.acceptance_criteria || [],
      test_plan: task.spec.test_plan || '',
      risk_assessment: task.spec.risk_assessment || task.spec.risk || 'low',
      rollback_plan: task.spec.rollback_plan || '',
      required_approvals: task.spec.required_approvals || [],
      estimated_cost: task.spec.estimated_cost || task.max_cost || 0,
      recommended_agent: task.spec.recommended_agent || 'implementer',
      generated_by: task.spec.generated_by || 'planner',
    };
  }
  return null;
}

export default function TaskDetailPage() {
  const params = useParams();
  const taskId = params.id as string;
  const queryClient = useQueryClient();

  const { data: task, isLoading: taskLoading } = useQuery({
    queryKey: ['task', taskId],
    queryFn: () => api.getTask(taskId),
  });

  const { data: runs } = useQuery({
    queryKey: ['runs', taskId],
    queryFn: () => api.listRuns(taskId),
    enabled: !!taskId,
  });

  const { data: approvals } = useQuery({
    queryKey: ['approvals', taskId],
    queryFn: () => api.listApprovals(taskId),
    enabled: !!taskId,
  });

  const { data: workspaces } = useQuery({
    queryKey: ['workspaces', taskId],
    queryFn: () => api.listWorkspaces(taskId),
    enabled: !!taskId,
  });

  const generateSpecMutation = useMutation({
    mutationFn: () => api.generateSpec(taskId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      queryClient.invalidateQueries({ queryKey: ['spec', taskId] });
    },
  });

  const retryMutation = useMutation({
    mutationFn: () => api.updateTask(taskId, { status: 'backlog' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
    },
  });

  if (taskLoading) return <Loading />;

  const runList = runs?.data || runs || [];
  const approvalList = approvals?.data || approvals || [];
  const workspaceList = workspaces?.data || workspaces || [];
  const spec = parseSpec(task);

  const getPriorityColor = (p: string) => {
    switch (p) {
      case 'urgent': return 'bg-red-500/10 text-red-400 border-red-500/30';
      case 'high': return 'bg-orange-500/10 text-orange-400 border-orange-500/30';
      case 'medium': return 'bg-yellow-500/10 text-yellow-400 border-yellow-500/30';
      default: return 'bg-gray-500/10 text-gray-400 border-gray-500/30';
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link
          href={`/projects/${task?.project_id}`}
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          Back to Project
        </Link>
        <div className="flex items-start justify-between flex-wrap gap-4">
          <div>
            <div className="flex items-center gap-3 mb-2 flex-wrap">
              <StatusBadge status={task?.status} />
              <Badge
                variant="outline"
                className={getPriorityColor(task?.priority)}
              >
                {task?.priority}
              </Badge>
              {task?.risk_level !== 'low' && (
                <Badge
                  variant="outline"
                  className="bg-red-500/10 text-red-400 border-red-500/30"
                >
                  <AlertTriangle className="w-3 h-3 mr-1" />
                  {task?.risk_level} risk
                </Badge>
              )}
            </div>
            <h1 className="text-2xl font-bold text-white">{task?.title}</h1>
            {task?.description && (
              <p className="text-gray-400 mt-2 max-w-3xl">{task?.description}</p>
            )}
          </div>
          <TaskActions
            taskId={taskId}
            status={task?.status}
            workspaceId={task?.workspace_id || workspaceList[0]?.id}
            onGenerateSpec={() => generateSpecMutation.mutate()}
            onRetry={() => retryMutation.mutate()}
            isGeneratingSpec={generateSpecMutation.isPending}
          />
        </div>
      </div>

      {/* Metadata cards */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <Clock className="w-3 h-3" />
            Created
          </div>
          <TimeAgo date={task?.created_at} className="text-white font-medium" />
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <GitCommit className="w-3 h-3" />
            Branch
          </div>
          <div className="text-white font-medium truncate">{task?.target_branch || 'main'}</div>
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <DollarSign className="w-3 h-3" />
            Max Cost
          </div>
          <div className="text-white font-medium">
            {task?.max_cost ? `$${task.max_cost}` : 'Unlimited'}
          </div>
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <Layers className="w-3 h-3" />
            Source
          </div>
          <div className="text-white font-medium capitalize">{task?.source || 'web'}</div>
        </Card>
      </div>

      {/* Spec Preview Section */}
      {spec && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-3">Specification</h2>
          <TaskSpecPreview taskId={taskId} spec={spec} />
        </div>
      )}

      {/* Generate Spec button for backlog */}
      {task?.status === 'backlog' && !spec && (
        <Card className="p-6 text-center">
          <div className="text-gray-500 mb-3">
            No specification generated yet. Generate one to review the implementation plan.
          </div>
          <button
            onClick={() => generateSpecMutation.mutate()}
            disabled={generateSpecMutation.isPending}
            className="btn-primary"
          >
            {generateSpecMutation.isPending ? 'Generating...' : 'Generate Spec'}
          </button>
        </Card>
      )}

      {/* Workspace Section */}
      {workspaceList.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-3">Workspaces</h2>
          <div className="space-y-2">
            {workspaceList.map((ws: any) => (
              <Card key={ws.id}>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <FolderGit className="w-5 h-5 text-blue-400" />
                    <div>
                      <div className="text-white font-medium">{ws.name}</div>
                      <div className="text-xs text-gray-500">
                        Branch: {ws.branch} · Status: {ws.status}
                      </div>
                    </div>
                  </div>
                  <Link
                    href={`/workspaces/${ws.id}`}
                    className="btn-secondary text-sm flex items-center gap-1"
                  >
                    <ExternalLink className="w-3 h-3" />
                    Open IDE
                  </Link>
                </div>
              </Card>
            ))}
          </div>
        </div>
      )}

      {/* Agent Runs */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-lg font-semibold text-white">Agent Runs</h2>
          <Link
            href={`/tasks/${taskId}/runs`}
            className="text-sm text-blue-400 hover:text-blue-300"
          >
            View All
          </Link>
        </div>
        <div className="space-y-2">
          {runList.map((run: any) => (
            <Card key={run.id}>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <StatusBadge status={run.status} />
                  <span className="text-white font-medium capitalize">
                    {run.agent_role.replace('_', ' ')}
                  </span>
                  {run.model && (
                    <span className="text-xs text-gray-500">{run.model}</span>
                  )}
                </div>
                <div className="flex items-center gap-4 text-sm text-gray-500">
                  <CostBadge cost={run.total_cost} />
                  <span>{run.prompt_tokens + run.completion_tokens} tokens</span>
                  <TimeAgo date={run.created_at} />
                  <Link
                    href={`/runs/${run.id}`}
                    className="text-blue-400 hover:text-blue-300 text-xs"
                  >
                    View
                  </Link>
                </div>
              </div>
            </Card>
          ))}
          {runList.length === 0 && (
            <div className="text-center py-8 text-gray-500">
              No runs yet. Start the task to begin execution.
            </div>
          )}
        </div>
      </div>

      {/* Approvals */}
      {approvalList.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-3">Approvals</h2>
          <div className="space-y-2">
            {approvalList.map((approval: any) => (
              <Card key={approval.id}>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    {approval.response === 'approved' ? (
                      <CheckCircle className="w-5 h-5 text-green-400" />
                    ) : approval.response === 'rejected' ? (
                      <XCircle className="w-5 h-5 text-red-400" />
                    ) : (
                      <Clock className="w-5 h-5 text-yellow-400" />
                    )}
                    <div>
                      <span className="text-white font-medium capitalize">
                        {approval.approval_type.replace('_', ' ')}
                      </span>
                      <div className="text-xs text-gray-500">
                        {approval.response
                          ? `${approval.response} by ${approval.responded_by}`
                          : 'Pending approval'}
                      </div>
                    </div>
                  </div>
                  <TimeAgo date={approval.created_at} />
                </div>
              </Card>
            ))}
          </div>
        </div>
      )}

      {/* PR Section */}
      {task?.status === 'pr_created' && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-3">Pull Request</h2>
          <Card>
            <div className="flex items-center gap-3">
              <GitPullRequest className="w-5 h-5 text-purple-400" />
              <div>
                <div className="text-white font-medium">PR Created</div>
                <div className="text-xs text-gray-500">
                  A pull request has been created for this task.
                </div>
              </div>
              <Link
                href={`/tasks/${taskId}`}
                className="btn-secondary text-sm flex items-center gap-1 ml-auto"
              >
                <ExternalLink className="w-3 h-3" />
                View PR
              </Link>
            </div>
          </Card>
        </div>
      )}
    </div>
  );
}
