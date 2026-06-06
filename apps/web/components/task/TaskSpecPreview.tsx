'use client';

import React from 'react';
import type { TaskSpec } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { AlertTriangle, DollarSign, Bot, FileText } from 'lucide-react';
import Link from 'next/link';

interface TaskSpecPreviewProps {
  taskId: string;
  spec: TaskSpec;
}

function RiskBadge({ level }: { level: string }) {
  const config: Record<string, { color: string; bg: string }> = {
    low: { color: 'text-green-400', bg: 'bg-green-500/10 border-green-500/30' },
    medium: { color: 'text-yellow-400', bg: 'bg-yellow-500/10 border-yellow-500/30' },
    high: { color: 'text-orange-400', bg: 'bg-orange-500/10 border-orange-500/30' },
    critical: { color: 'text-red-400', bg: 'bg-red-500/10 border-red-500/30' },
  };
  const c = config[level] || config.low;
  return (
    <Badge variant="outline" className={`${c.bg} ${c.color} capitalize text-xs`}>
      <AlertTriangle className="w-3 h-3 mr-1" />
      {level}
    </Badge>
  );
}

export function TaskSpecPreview({ taskId, spec }: TaskSpecPreviewProps) {
  const riskLevel = spec.risk_assessment?.toLowerCase().includes('critical')
    ? 'critical'
    : spec.risk_assessment?.toLowerCase().includes('high')
    ? 'high'
    : spec.risk_assessment?.toLowerCase().includes('medium')
    ? 'medium'
    : 'low';

  return (
    <Card className="p-4">
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-2">
          <FileText className="w-4 h-4 text-blue-400" />
          <h3 className="text-sm font-medium text-white">Generated Spec</h3>
        </div>
        <Link
          href={`/tasks/${taskId}/spec`}
          className="text-xs text-blue-400 hover:text-blue-300 flex items-center gap-1"
        >
          View Full Spec
        </Link>
      </div>

      {/* Summary - 2 lines */}
      <p className="text-sm text-gray-400 line-clamp-2 mb-3">{spec.summary}</p>

      {/* Badges */}
      <div className="flex items-center gap-2 flex-wrap">
        <RiskBadge level={riskLevel} />
        <Badge variant="outline" className="bg-blue-500/10 text-blue-400 border-blue-500/30 text-xs">
          <DollarSign className="w-3 h-3 mr-1" />
          Est. ${spec.estimated_cost?.toFixed(4) || '0.0000'}
        </Badge>
        {spec.recommended_agent && (
          <Badge variant="outline" className="bg-purple-500/10 text-purple-400 border-purple-500/30 text-xs">
            <Bot className="w-3 h-3 mr-1" />
            {spec.recommended_agent}
          </Badge>
        )}
      </div>

      {/* File counts */}
      <div className="flex items-center gap-4 mt-3 text-xs text-gray-500">
        <span>{(spec.files_to_change || []).length} files to change</span>
        <span>{(spec.files_to_create || []).length} files to create</span>
        <span>{(spec.acceptance_criteria || []).length} criteria</span>
      </div>
    </Card>
  );
}
