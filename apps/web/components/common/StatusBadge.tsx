'use client';

import React from 'react';
import { cn } from '@/lib/utils';
import {
  Circle,
  CheckCircle2,
  XCircle,
  Clock,
  Loader2,
  GitPullRequest,
  AlertTriangle,
  Play,
  Pause,
  Ban,
  HelpCircle,
  Plug,
  Unplug,
  Shield,
  ShieldAlert,
} from 'lucide-react';

interface StatusBadgeProps {
  status: string;
  size?: 'sm' | 'md';
  className?: string;
}

const statusConfig: Record<string, { label: string; icon: React.ElementType; classes: string }> = {
  // Task statuses
  backlog: { label: 'Backlog', icon: Circle, classes: 'bg-gray-800 text-gray-400 border-gray-700' },
  spec_review: { label: 'Spec Review', icon: ShieldAlert, classes: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/30' },
  approved: { label: 'Approved', icon: Shield, classes: 'bg-green-500/10 text-green-400 border-green-500/30' },
  running: { label: 'Running', icon: Loader2, classes: 'bg-blue-500/10 text-blue-400 border-blue-500/30' },
  reviewing: { label: 'Reviewing', icon: Clock, classes: 'bg-purple-500/10 text-purple-400 border-purple-500/30' },
  pr_created: { label: 'PR Created', icon: GitPullRequest, classes: 'bg-cyan-500/10 text-cyan-400 border-cyan-500/30' },
  done: { label: 'Done', icon: CheckCircle2, classes: 'bg-green-500/10 text-green-400 border-green-500/30' },
  failed: { label: 'Failed', icon: XCircle, classes: 'bg-red-500/10 text-red-400 border-red-500/30' },
  cancelled: { label: 'Cancelled', icon: Ban, classes: 'bg-gray-800 text-gray-500 border-gray-700' },
  // Run statuses
  pending: { label: 'Pending', icon: Clock, classes: 'bg-gray-800 text-gray-400 border-gray-700' },
  paused: { label: 'Paused', icon: Pause, classes: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/30' },
  completed: { label: 'Completed', icon: CheckCircle2, classes: 'bg-green-500/10 text-green-400 border-green-500/30' },
  // Connection statuses
  connected: { label: 'Connected', icon: Plug, classes: 'bg-green-500/10 text-green-400 border-green-500/30' },
  disconnected: { label: 'Disconnected', icon: Unplug, classes: 'bg-gray-800 text-gray-500 border-gray-700' },
  error: { label: 'Error', icon: AlertTriangle, classes: 'bg-red-500/10 text-red-400 border-red-500/30' },
  // Integration statuses
  active: { label: 'Active', icon: CheckCircle2, classes: 'bg-green-500/10 text-green-400 border-green-500/30' },
  inactive: { label: 'Inactive', icon: Circle, classes: 'bg-gray-800 text-gray-500 border-gray-700' },
};

export function StatusBadge({ status, size = 'md', className }: StatusBadgeProps) {
  const config = statusConfig[status] || {
    label: status,
    icon: HelpCircle,
    classes: 'bg-gray-800 text-gray-400 border-gray-700',
  };

  const Icon = config.icon;
  const isRunning = status === 'running';

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border font-medium',
        size === 'sm' ? 'px-1.5 py-0.5 text-[10px]' : 'px-2 py-0.5 text-xs',
        config.classes,
        className
      )}
    >
      <Icon className={cn(size === 'sm' ? 'w-2.5 h-2.5' : 'w-3 h-3', isRunning && 'animate-spin')} />
      {config.label}
    </span>
  );
}
