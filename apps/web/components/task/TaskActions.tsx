'use client';

import React from 'react';
import type { TaskStatus } from '@/lib/types';
import Link from 'next/link';
import {
  Play,
  FileText,
  Activity,
  Eye,
  GitPullRequest,
  CheckCircle,
  RotateCcw,
  ScrollText,
  Sparkles,
} from 'lucide-react';

interface TaskActionsProps {
  taskId: string;
  status: TaskStatus;
  workspaceId?: string;
  onGenerateSpec?: () => void;
  onRetry?: () => void;
  isGeneratingSpec?: boolean;
}

export function TaskActions({
  taskId,
  status,
  workspaceId,
  onGenerateSpec,
  onRetry,
  isGeneratingSpec,
}: TaskActionsProps) {
  const actions: Record<TaskStatus, React.ReactNode> = {
    backlog: (
      <button
        onClick={onGenerateSpec}
        disabled={isGeneratingSpec}
        className="btn-primary flex items-center gap-2"
      >
        <Sparkles className="w-4 h-4" />
        {isGeneratingSpec ? 'Generating...' : 'Generate Spec'}
      </button>
    ),
    spec_review: (
      <Link href={`/tasks/${taskId}/spec`} className="btn-primary flex items-center gap-2">
        <FileText className="w-4 h-4" />
        Review Spec
      </Link>
    ),
    approved: (
      <Link href={`/tasks/${taskId}/runs`} className="btn-primary flex items-center gap-2">
        <Activity className="w-4 h-4" />
        View Progress
      </Link>
    ),
    running: (
      <Link href={`/tasks/${taskId}/runs`} className="btn-primary flex items-center gap-2">
        <Activity className="w-4 h-4" />
        View Live Run
      </Link>
    ),
    reviewing: (
      <Link href={`/tasks/${taskId}/runs`} className="btn-primary flex items-center gap-2">
        <Eye className="w-4 h-4" />
        View Review
      </Link>
    ),
    pr_created: (
      <Link href={`/tasks/${taskId}`} className="btn-primary flex items-center gap-2">
        <GitPullRequest className="w-4 h-4" />
        View PR
      </Link>
    ),
    done: (
      <Link href={`/tasks/${taskId}`} className="btn-secondary flex items-center gap-2">
        <CheckCircle className="w-4 h-4" />
        View Summary
      </Link>
    ),
    failed: (
      <div className="flex items-center gap-2">
        <button onClick={onRetry} className="btn-primary flex items-center gap-2">
          <RotateCcw className="w-4 h-4" />
          Retry
        </button>
        <Link href={`/tasks/${taskId}/runs`} className="btn-secondary flex items-center gap-2">
          <ScrollText className="w-4 h-4" />
          View Logs
        </Link>
      </div>
    ),
    cancelled: (
      <button onClick={onRetry} className="btn-primary flex items-center gap-2">
        <RotateCcw className="w-4 h-4" />
        Restart
      </button>
    ),
  };

  const workspaceLink = workspaceId && (status === 'running' || status === 'reviewing') ? (
    <Link
      href={`/workspaces/${workspaceId}`}
      className="btn-secondary flex items-center gap-2"
    >
      <Play className="w-4 h-4" />
      Open Workspace
    </Link>
  ) : null;

  return (
    <div className="flex items-center gap-2 flex-wrap">
      {actions[status] || null}
      {workspaceLink}
    </div>
  );
}
