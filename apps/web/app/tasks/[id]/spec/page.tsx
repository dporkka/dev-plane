'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useParams, useRouter } from 'next/navigation';
import { Card } from '@/components/ui/card';
import { Loading } from '@/components/common/Loading';
import { StatusBadge } from '@/components/common/StatusBadge';
import { SpecReview } from '@/components/task/SpecReview';
import type { TaskSpec } from '@/lib/types';
import {
  ArrowLeft,
  FileText,
  AlertCircle,
} from 'lucide-react';
import Link from 'next/link';

// Parse spec from task data into structured TaskSpec
function parseSpec(task: any): TaskSpec | null {
  if (!task?.spec) return null;

  // If spec is already a structured object with required fields
  if (typeof task.spec === 'object' && task.spec.summary) {
    return {
      id: task.spec.id || `spec-${task.id}`,
      task_id: task.id,
      summary: task.spec.summary || task.title,
      problem_statement: task.spec.problem_statement || task.spec.problem || task.description || '',
      implementation_plan: task.spec.implementation_plan || task.spec.plan || [],
      files_to_change: task.spec.files_to_change || [],
      files_to_create: task.spec.files_to_create || [],
      acceptance_criteria: task.spec.acceptance_criteria || task.spec.acceptance_criteria || [],
      test_plan: task.spec.test_plan || '',
      risk_assessment: task.spec.risk_assessment || task.spec.risk || 'low',
      rollback_plan: task.spec.rollback_plan || '',
      required_approvals: task.spec.required_approvals || [],
      estimated_cost: task.spec.estimated_cost || task.max_cost || 0,
      recommended_agent: task.spec.recommended_agent || 'implementer',
      generated_by: task.spec.generated_by || 'planner',
    };
  }

  // If spec is a string, wrap it
  const specStr = typeof task.spec === 'string' ? task.spec : JSON.stringify(task.spec, null, 2);
  return {
    id: `spec-${task.id}`,
    task_id: task.id,
    summary: task.title,
    problem_statement: task.description || specStr,
    implementation_plan: [],
    files_to_change: [],
    files_to_create: [],
    acceptance_criteria: task.acceptance_criteria || [],
    test_plan: '',
    risk_assessment: task.risk_level || 'low',
    rollback_plan: '',
    required_approvals: task.approval_requirements || [],
    estimated_cost: task.max_cost || 0,
    recommended_agent: 'implementer',
    generated_by: 'planner',
  };
}

export default function SpecReviewPage() {
  const params = useParams();
  const taskId = params.id as string;
  const router = useRouter();
  const queryClient = useQueryClient();
  const [editError, setEditError] = useState<string | null>(null);

  const { data: task, isLoading } = useQuery({
    queryKey: ['task', taskId],
    queryFn: () => api.getTask(taskId),
  });

  const { data: specData } = useQuery({
    queryKey: ['spec', taskId],
    queryFn: () => api.getTaskSpec(taskId),
    enabled: !!taskId,
    retry: false,
  });

  const approveMutation = useMutation({
    mutationFn: () => api.approveSpec(taskId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      router.push(`/tasks/${taskId}`);
    },
  });

  const rejectMutation = useMutation({
    mutationFn: () => api.cancelTask(taskId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      router.push(`/tasks/${taskId}`);
    },
  });

  const editMutation = useMutation({
    mutationFn: (editedSpec: TaskSpec) => api.approveSpec(taskId, editedSpec),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['task', taskId] });
      queryClient.invalidateQueries({ queryKey: ['spec', taskId] });
      setEditError(null);
    },
    onError: (err: any) => {
      setEditError(err.message || 'Failed to save spec changes');
    },
  });

  if (isLoading) return <Loading />;

  // Merge task spec with fetched spec data
  const mergedTask = specData ? { ...task, spec: specData } : task;
  const spec = parseSpec(mergedTask);

  return (
    <div className="space-y-6 max-w-4xl">
      {/* Header */}
      <div>
        <Link
          href={`/tasks/${taskId}`}
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          Back to Task
        </Link>
        <div className="flex items-center justify-between">
          <div>
            <div className="flex items-center gap-3 mb-2">
              <FileText className="w-5 h-5 text-blue-400" />
              <h1 className="text-2xl font-bold text-white">Spec Review</h1>
            </div>
            <p className="text-gray-500">{task?.title}</p>
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge status={task?.status} />
          </div>
        </div>
      </div>

      {/* Error alert */}
      {editError && (
        <Card className="p-3 bg-red-500/10 border-red-500/30">
          <div className="flex items-center gap-2 text-red-400 text-sm">
            <AlertCircle className="w-4 h-4" />
            {editError}
          </div>
        </Card>
      )}

      {/* Spec not found */}
      {!spec && (
        <Card className="p-8 text-center">
          <FileText className="w-12 h-12 text-gray-600 mx-auto mb-3" />
          <h2 className="text-lg font-medium text-white mb-2">No Spec Found</h2>
          <p className="text-gray-500 text-sm mb-4">
            The spec hasn&apos;t been generated yet. Generate one from the task page.
          </p>
          <Link href={`/tasks/${taskId}`} className="btn-primary">
            Back to Task
          </Link>
        </Card>
      )}

      {/* Spec Review */}
      {spec && (
        <SpecReview
          spec={spec}
          onApprove={task?.status === 'spec_review' ? () => approveMutation.mutate() : undefined}
          onEdit={(edited) => editMutation.mutate(edited)}
          onReject={task?.status === 'spec_review' ? () => rejectMutation.mutate() : undefined}
          isApproving={approveMutation.isPending}
        />
      )}
    </div>
  );
}
