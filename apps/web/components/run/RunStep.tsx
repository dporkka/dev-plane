'use client';

import React from 'react';
import { cn } from '@/lib/utils';
import {
  Brain,
  Wrench,
  Terminal,
  FileDiff,
  AlertCircle,
  MessageSquare,
  CheckCircle,
  Loader2,
  XCircle,
} from 'lucide-react';

interface RunStepProps {
  stepNumber: number;
  type: string;
  content: string;
  status: string;
  toolName?: string;
  command?: string;
  cost?: number;
  latency?: number;
}

const typeConfig: Record<string, { icon: React.ElementType; label: string; color: string }> = {
  thought: { icon: Brain, label: 'Thought', color: 'text-purple-400' },
  tool_call: { icon: Wrench, label: 'Tool', color: 'text-blue-400' },
  command_run: { icon: Terminal, label: 'Command', color: 'text-yellow-400' },
  file_patch: { icon: FileDiff, label: 'Patch', color: 'text-green-400' },
  approval_request: { icon: AlertCircle, label: 'Approval', color: 'text-orange-400' },
  message: { icon: MessageSquare, label: 'Message', color: 'text-gray-400' },
  error: { icon: XCircle, label: 'Error', color: 'text-red-400' },
};

const statusConfig: Record<string, { icon: React.ElementType; color: string }> = {
  pending: { icon: Loader2, color: 'text-gray-500' },
  running: { icon: Loader2, color: 'text-blue-400' },
  completed: { icon: CheckCircle, color: 'text-green-400' },
  failed: { icon: XCircle, color: 'text-red-400' },
};

export function RunStep({
  stepNumber,
  type,
  content,
  status,
  toolName,
  cost,
  latency,
}: RunStepProps) {
  const config = typeConfig[type] || typeConfig.message;
  const statusConfig2 = statusConfig[status] || statusConfig.pending;
  const Icon = config.icon;
  const StatusIcon = statusConfig2.icon;
  const isRunning = status === 'running';

  return (
    <div
      className={cn(
        'flex items-start gap-3 py-2 px-3 rounded-md transition-colors',
        isRunning && 'bg-blue-500/5'
      )}
    >
      {/* Step number */}
      <div className="flex-shrink-0 w-6 text-xs text-gray-600 text-right pt-0.5">
        {stepNumber}
      </div>

      {/* Type icon */}
      <div className={cn('flex-shrink-0 mt-0.5', config.color)}>
        <Icon className="w-4 h-4" />
      </div>

      {/* Content */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-gray-500">
            {config.label}
          </span>
          {toolName && (
            <span className="text-[10px] bg-[#21262d] text-gray-400 px-1.5 py-0.5 rounded">
              {toolName}
            </span>
          )}
        </div>
        <p className="text-sm text-gray-300 mt-0.5">{content}</p>
      </div>

      {/* Meta */}
      <div className="flex items-center gap-3 flex-shrink-0">
        {cost !== undefined && cost > 0 && (
          <span className="text-xs text-gray-600">
            ${cost.toFixed(4)}
          </span>
        )}
        {latency !== undefined && (
          <span className="text-xs text-gray-600">
            {(latency / 1000).toFixed(1)}s
          </span>
        )}
        <div className={cn('flex-shrink-0', statusConfig2.color)}>
          <StatusIcon className={cn('w-4 h-4', isRunning && 'animate-spin')} />
        </div>
      </div>
    </div>
  );
}
