'use client';

import React from 'react';
import type { DashboardStats } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { SkeletonStats } from '@/components/ui/skeleton';
import {
  Activity,
  ListTodo,
  DollarSign,
  AlertCircle,
} from 'lucide-react';

interface StatsCardsProps {
  data?: DashboardStats;
  isLoading?: boolean;
}

export function StatsCards({ data, isLoading }: StatsCardsProps) {
  if (isLoading || !data) {
    return <SkeletonStats />;
  }

  const stats = [
    {
      label: 'Active Runs',
      value: data.active_runs,
      icon: Activity,
      color: 'text-blue-400',
      bg: 'bg-blue-500/10',
    },
    {
      label: 'Tasks Today',
      value: data.tasks_today,
      icon: ListTodo,
      color: 'text-green-400',
      bg: 'bg-green-500/10',
    },
    {
      label: 'Cost Today',
      value: `$${data.cost_today.toFixed(2)}`,
      icon: DollarSign,
      color: 'text-yellow-400',
      bg: 'bg-yellow-500/10',
    },
    {
      label: 'Pending Approvals',
      value: data.pending_approvals,
      icon: AlertCircle,
      color: 'text-red-400',
      bg: 'bg-red-500/10',
    },
  ];

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
      {stats.map((stat) => (
        <Card key={stat.label}>
          <div className="flex items-center gap-3">
            <div className={`p-2 rounded-lg ${stat.bg}`}>
              <stat.icon className={`w-5 h-5 ${stat.color}`} />
            </div>
            <div>
              <div className="text-2xl font-bold text-white">{stat.value}</div>
              <div className="text-xs text-gray-500">{stat.label}</div>
            </div>
          </div>
        </Card>
      ))}
    </div>
  );
}
