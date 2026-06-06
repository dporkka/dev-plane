'use client';

import React from 'react';
import type { Task } from '@/lib/types';
import { StatusBadge } from '@/components/common/StatusBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import { Badge } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import { AlertTriangle, Clock, GitBranch, DollarSign } from 'lucide-react';

interface TaskDetailProps {
  task: Task;
}

export function TaskDetail({ task }: TaskDetailProps) {
  const getPriorityColor = (p: string) => {
    switch (p) {
      case 'urgent': return 'bg-red-500/10 text-red-400 border-red-500/30';
      case 'high': return 'bg-orange-500/10 text-orange-400 border-orange-500/30';
      case 'medium': return 'bg-yellow-500/10 text-yellow-400 border-yellow-500/30';
      default: return 'bg-gray-500/10 text-gray-400 border-gray-500/30';
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3 flex-wrap">
        <StatusBadge status={task.status} />
        <Badge variant="outline" className={getPriorityColor(task.priority)}>
          {task.priority}
        </Badge>
        {task.risk_level !== 'low' && (
          <Badge variant="outline" className="bg-red-500/10 text-red-400 border-red-500/30">
            <AlertTriangle className="w-3 h-3 mr-1" />
            {task.risk_level} risk
          </Badge>
        )}
      </div>

      <h1 className="text-xl font-bold text-white">{task.title}</h1>

      {task.description && (
        <p className="text-gray-400 text-sm">{task.description}</p>
      )}

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Card className="p-3">
          <div className="flex items-center gap-1.5 text-gray-500 text-xs mb-1">
            <Clock className="w-3 h-3" />
            Created
          </div>
          <TimeAgo date={task.created_at} className="text-white text-sm" />
        </Card>
        <Card className="p-3">
          <div className="flex items-center gap-1.5 text-gray-500 text-xs mb-1">
            <GitBranch className="w-3 h-3" />
            Branch
          </div>
          <div className="text-white text-sm truncate">{task.target_branch || 'main'}</div>
        </Card>
        <Card className="p-3">
          <div className="flex items-center gap-1.5 text-gray-500 text-xs mb-1">
            <DollarSign className="w-3 h-3" />
            Max Cost
          </div>
          <div className="text-white text-sm">
            {task.max_cost ? `$${task.max_cost}` : 'Unlimited'}
          </div>
        </Card>
        <Card className="p-3">
          <div className="flex items-center gap-1.5 text-gray-500 text-xs mb-1">
            <Clock className="w-3 h-3" />
            Timeout
          </div>
          <div className="text-white text-sm">{task.max_runtime_minutes} min</div>
        </Card>
      </div>
    </div>
  );
}
