'use client';

import React from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { Approval, ApprovalType, ApprovalResponse } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { SkeletonCard } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { TimeAgo } from '@/components/common/TimeAgo';
import {
  AlertCircle,
  CheckCircle,
  XCircle,
  Clock,
  Shield,
  FileText,
  Play,
  Rocket,
} from 'lucide-react';

interface PendingApprovalsWidgetProps {
  approvals: Approval[];
  isLoading?: boolean;
}

const typeIcons: Record<string, React.ElementType> = {
  spec: FileText,
  execution: Play,
  deploy: Rocket,
  risky_action: Shield,
};

const typeColors: Record<string, string> = {
  spec: 'bg-blue-500/10 text-blue-400 border-blue-500/30',
  execution: 'bg-purple-500/10 text-purple-400 border-purple-500/30',
  deploy: 'bg-green-500/10 text-green-400 border-green-500/30',
  risky_action: 'bg-red-500/10 text-red-400 border-red-500/30',
};

export function PendingApprovalsWidget({ approvals, isLoading }: PendingApprovalsWidgetProps) {
  const queryClient = useQueryClient();

  const respondMutation = useMutation({
    mutationFn: ({ id, response, note }: { id: string; response: ApprovalResponse; note?: string }) =>
      api.respondApproval(id, response, note),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['approvals'] });
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    },
  });

  if (isLoading) {
    return (
      <div>
        <h2 className="text-lg font-semibold text-white mb-3 flex items-center gap-2">
          <AlertCircle className="w-5 h-5 text-yellow-400" />
          Pending Approvals
        </h2>
        <div className="space-y-2">
          {[1, 2].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    );
  }

  const pending = approvals.filter((a) => !a.response);

  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        <AlertCircle className="w-5 h-5 text-yellow-400" />
        <h2 className="text-lg font-semibold text-white">Pending Approvals</h2>
        {pending.length > 0 && (
          <span className="text-xs bg-yellow-500/10 text-yellow-400 px-2 py-0.5 rounded-full">
            {pending.length}
          </span>
        )}
      </div>
      <div className="space-y-2">
        {pending.map((approval) => {
          const Icon = typeIcons[approval.approval_type] || Shield;
          const colorClass = typeColors[approval.approval_type] || typeColors.risky_action;

          return (
            <Card key={approval.id} className="py-3">
              <div className="flex items-start justify-between gap-3">
                <div className="flex items-start gap-3 min-w-0">
                  <div className="p-1.5 rounded bg-[#21262d] flex-shrink-0 mt-0.5">
                    <Icon className="w-4 h-4 text-gray-400" />
                  </div>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className={`${colorClass} text-xs capitalize`}>
                        {approval.approval_type.replace('_', ' ')}
                      </Badge>
                      <span className="text-xs text-gray-500">
                        <TimeAgo date={approval.created_at} />
                      </span>
                    </div>
                    <p className="text-sm text-gray-300 mt-1">
                      Task: {approval.task_id?.slice(0, 8)}...
                    </p>
                    <p className="text-xs text-gray-600">
                      Requested by {approval.requested_by}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-1 flex-shrink-0">
                  <button
                    onClick={() =>
                      respondMutation.mutate({ id: approval.id, response: 'approved' })
                    }
                    disabled={respondMutation.isPending}
                    className="p-1.5 rounded hover:bg-green-500/10 text-gray-500 hover:text-green-400 transition-colors"
                    title="Approve"
                  >
                    <CheckCircle className="w-4 h-4" />
                  </button>
                  <button
                    onClick={() =>
                      respondMutation.mutate({ id: approval.id, response: 'rejected' })
                    }
                    disabled={respondMutation.isPending}
                    className="p-1.5 rounded hover:bg-red-500/10 text-gray-500 hover:text-red-400 transition-colors"
                    title="Reject"
                  >
                    <XCircle className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </Card>
          );
        })}
        {pending.length === 0 && (
          <div className="text-center py-8 text-gray-500 text-sm">
            No pending approvals
          </div>
        )}
      </div>
    </div>
  );
}
