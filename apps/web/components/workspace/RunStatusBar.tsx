'use client';

import React from 'react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import {
  Loader2,
  CheckCircle,
  XCircle,
  Pause,
  Clock,
  DollarSign,
  Zap,
  Activity,
} from 'lucide-react';

export interface RunStatus {
  status: 'running' | 'paused' | 'completed' | 'failed' | 'idle';
  currentStep: number;
  totalSteps: number;
  currentAction: string;
  cost: number;
  elapsedSeconds: number;
  model?: string;
}

interface RunStatusBarProps {
  runStatus: RunStatus;
}

function formatElapsed(seconds: number): string {
  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
}

export function RunStatusBar({ runStatus }: RunStatusBarProps) {
  const progress = runStatus.totalSteps > 0
    ? (runStatus.currentStep / runStatus.totalSteps) * 100
    : 0;

  const statusConfig: Record<string, { color: string; icon: React.ElementType; label: string }> = {
    running: { color: 'text-blue-400 bg-blue-500/10 border-blue-500/30', icon: Loader2, label: 'Running' },
    paused: { color: 'text-yellow-400 bg-yellow-500/10 border-yellow-500/30', icon: Pause, label: 'Paused' },
    completed: { color: 'text-green-400 bg-green-500/10 border-green-500/30', icon: CheckCircle, label: 'Completed' },
    failed: { color: 'text-red-400 bg-red-500/10 border-red-500/30', icon: XCircle, label: 'Failed' },
    idle: { color: 'text-gray-400 bg-gray-500/10 border-gray-500/30', icon: Clock, label: 'Idle' },
  };

  const config = statusConfig[runStatus.status] || statusConfig.idle;
  const StatusIcon = config.icon;

  return (
    <div className="h-10 bg-[#161b22] border-t border-[#30363d] flex items-center px-3 gap-4 text-xs flex-shrink-0">
      {/* Status badge */}
      <Badge variant="outline" className={config.color}>
        <StatusIcon className={cn('w-3 h-3 mr-1', runStatus.status === 'running' && 'animate-spin')} />
        {config.label}
      </Badge>

      {/* Progress bar */}
      <div className="flex items-center gap-2 flex-1 min-w-0">
        <div className="flex-1 h-1.5 bg-[#30363d] rounded-full overflow-hidden">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-500',
              runStatus.status === 'running' ? 'bg-blue-500' :
              runStatus.status === 'completed' ? 'bg-green-500' :
              runStatus.status === 'failed' ? 'bg-red-500' :
              'bg-gray-600'
            )}
            style={{ width: `${Math.min(progress, 100)}%` }}
          />
        </div>
        <span className="text-gray-500 flex-shrink-0">
          Step {runStatus.currentStep} of {runStatus.totalSteps}
        </span>
      </div>

      {/* Current action */}
      {runStatus.currentAction && (
        <div className="flex items-center gap-1.5 text-gray-400 flex-shrink-0 max-w-[300px] truncate">
          <Zap className="w-3 h-3 text-yellow-400" />
          <span className="truncate">{runStatus.currentAction}</span>
        </div>
      )}

      {/* Cost */}
      <div className="flex items-center gap-1 text-gray-500 flex-shrink-0">
        <DollarSign className="w-3 h-3" />
        <span>{runStatus.cost.toFixed(4)}</span>
      </div>

      {/* Timer */}
      <div className="flex items-center gap-1 text-gray-500 flex-shrink-0">
        <Clock className="w-3 h-3" />
        <span>{formatElapsed(runStatus.elapsedSeconds)}</span>
      </div>

      {/* Model */}
      {runStatus.model && (
        <div className="flex items-center gap-1 text-gray-600 flex-shrink-0">
          <Activity className="w-3 h-3" />
          <span>{runStatus.model}</span>
        </div>
      )}
    </div>
  );
}
