'use client';

import React from 'react';
import Link from 'next/link';
import type { Task } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import { SkeletonCard } from '@/components/ui/skeleton';
import { ListTodo } from 'lucide-react';

interface RecentTasksProps {
  tasks: Task[];
  isLoading?: boolean;
}

export function RecentTasks({ tasks, isLoading }: RecentTasksProps) {
  if (isLoading) {
    return (
      <div>
        <h2 className="text-lg font-semibold text-white mb-3">Recent Tasks</h2>
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        <ListTodo className="w-5 h-5 text-gray-400" />
        <h2 className="text-lg font-semibold text-white">Recent Tasks</h2>
      </div>
      <div className="space-y-2">
        {tasks.slice(0, 6).map((task) => (
          <Link key={task.id} href={`/tasks/${task.id}`}>
            <Card className="hover:border-blue-500/30 transition-colors cursor-pointer py-3">
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2 min-w-0">
                  <StatusBadge status={task.status} size="sm" />
                  <span className="text-sm text-gray-200 truncate">
                    {task.title}
                  </span>
                </div>
                <TimeAgo date={task.created_at} className="text-xs text-gray-500 flex-shrink-0" />
              </div>
            </Card>
          </Link>
        ))}
        {tasks.length === 0 && (
          <div className="text-center py-8 text-gray-500 text-sm">
            No tasks yet
          </div>
        )}
      </div>
    </div>
  );
}
