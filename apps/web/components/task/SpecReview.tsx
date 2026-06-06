'use client';

import React, { useState } from 'react';
import type { TaskSpec } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import {
  ChevronDown,
  ChevronRight,
  DollarSign,
  AlertTriangle,
  CheckCircle2,
  FileEdit,
  FilePlus,
  ListChecks,
  FlaskConical,
  ShieldAlert,
  RotateCcw,
  Sparkles,
  Bot,
} from 'lucide-react';

interface SpecReviewProps {
  spec: TaskSpec;
  onApprove?: () => void;
  onEdit?: (spec: TaskSpec) => void;
  onReject?: () => void;
  isApproving?: boolean;
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
    <Badge variant="outline" className={`${c.bg} ${c.color} capitalize`}>
      <AlertTriangle className="w-3 h-3 mr-1" />
      {level} risk
    </Badge>
  );
}

function CostBadge({ cost }: { cost: number }) {
  return (
    <Badge variant="outline" className="bg-blue-500/10 text-blue-400 border-blue-500/30">
      <DollarSign className="w-3 h-3 mr-1" />
      Est. ${cost.toFixed(4)}
    </Badge>
  );
}

function CollapsibleSection({
  title,
  icon: Icon,
  children,
  defaultOpen = true,
}: {
  title: string;
  icon: React.ElementType;
  children: React.ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center justify-between px-4 py-3 hover:bg-[#21262d]/50 transition-colors"
      >
        <div className="flex items-center gap-2">
          <Icon className="w-4 h-4 text-gray-400" />
          <span className="font-medium text-white">{title}</span>
        </div>
        {open ? (
          <ChevronDown className="w-4 h-4 text-gray-500" />
        ) : (
          <ChevronRight className="w-4 h-4 text-gray-500" />
        )}
      </button>
      {open && <div className="px-4 py-3 border-t border-[#30363d]">{children}</div>}
    </Card>
  );
}

export function SpecReview({ spec, onApprove, onEdit, onReject, isApproving }: SpecReviewProps) {
  const [editedSpec, setEditedSpec] = useState<TaskSpec | null>(null);
  const isEditing = !!editedSpec;
  const currentSpec = editedSpec || spec;

  const handleEdit = (field: keyof TaskSpec, value: any) => {
    if (!editedSpec) return;
    setEditedSpec({ ...editedSpec, [field]: value });
  };

  const startEditing = () => setEditedSpec({ ...spec });
  const cancelEditing = () => setEditedSpec(null);
  const saveEditing = () => {
    if (editedSpec && onEdit) {
      onEdit(editedSpec);
    }
    setEditedSpec(null);
  };

  return (
    <div className="space-y-4">
      {/* Header badges */}
      <div className="flex items-center gap-3 flex-wrap">
        <RiskBadge level={spec.risk_assessment?.toLowerCase().includes('critical') ? 'critical' : spec.risk_assessment?.toLowerCase().includes('high') ? 'high' : spec.risk_assessment?.toLowerCase().includes('medium') ? 'medium' : 'low'} />
        <CostBadge cost={spec.estimated_cost} />
        <Badge variant="outline" className="bg-purple-500/10 text-purple-400 border-purple-500/30">
          <Bot className="w-3 h-3 mr-1" />
          {spec.recommended_agent}
        </Badge>
        <Badge variant="outline" className="bg-gray-500/10 text-gray-400 border-gray-500/30">
          <Sparkles className="w-3 h-3 mr-1" />
          {spec.generated_by}
        </Badge>
      </div>

      {/* Edit controls */}
      {isEditing && (
        <div className="flex items-center gap-2">
          <button onClick={saveEditing} className="btn-primary text-sm flex items-center gap-1">
            <CheckCircle2 className="w-3 h-3" />
            Save Changes
          </button>
          <button onClick={cancelEditing} className="btn-secondary text-sm flex items-center gap-1">
            <RotateCcw className="w-3 h-3" />
            Cancel
          </button>
        </div>
      )}

      {/* Summary */}
      <CollapsibleSection title="Summary" icon={Sparkles}>
        {isEditing ? (
          <textarea
            value={currentSpec.summary}
            onChange={(e) => handleEdit('summary', e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[80px]"
          />
        ) : (
          <p className="text-gray-300 text-sm leading-relaxed">{spec.summary}</p>
        )}
      </CollapsibleSection>

      {/* Problem Statement */}
      <CollapsibleSection title="Problem Statement" icon={AlertTriangle}>
        {isEditing ? (
          <textarea
            value={currentSpec.problem_statement}
            onChange={(e) => handleEdit('problem_statement', e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[100px]"
          />
        ) : (
          <p className="text-gray-300 text-sm leading-relaxed whitespace-pre-wrap">{spec.problem_statement}</p>
        )}
      </CollapsibleSection>

      {/* Implementation Plan */}
      <CollapsibleSection title="Implementation Plan" icon={ListChecks}>
        {isEditing ? (
          <textarea
            value={(currentSpec.implementation_plan || []).join('\n')}
            onChange={(e) => handleEdit('implementation_plan', e.target.value.split('\n').filter(Boolean))}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[150px]"
            placeholder="One step per line"
          />
        ) : (
          <ol className="space-y-2">
            {(spec.implementation_plan || []).map((step, i) => (
              <li key={i} className="flex items-start gap-3 text-sm text-gray-300">
                <span className="flex-shrink-0 w-6 h-6 rounded-full bg-blue-500/10 text-blue-400 flex items-center justify-center text-xs font-medium">
                  {i + 1}
                </span>
                <span className="leading-relaxed pt-0.5">{step}</span>
              </li>
            ))}
          </ol>
        )}
      </CollapsibleSection>

      {/* Files to Change */}
      <CollapsibleSection title="Files to Change" icon={FileEdit}>
        {isEditing ? (
          <textarea
            value={(currentSpec.files_to_change || []).join('\n')}
            onChange={(e) => handleEdit('files_to_change', e.target.value.split('\n').filter(Boolean))}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[100px]"
            placeholder="One file path per line"
          />
        ) : (
          <ul className="space-y-1">
            {(spec.files_to_change || []).map((file, i) => (
              <li key={i} className="flex items-center gap-2 text-sm text-gray-300">
                <FileEdit className="w-3.5 h-3.5 text-yellow-400" />
                <code className="bg-[#21262d] px-1.5 py-0.5 rounded text-xs">{file}</code>
              </li>
            ))}
          </ul>
        )}
      </CollapsibleSection>

      {/* Files to Create */}
      <CollapsibleSection title="Files to Create" icon={FilePlus}>
        {isEditing ? (
          <textarea
            value={(currentSpec.files_to_create || []).join('\n')}
            onChange={(e) => handleEdit('files_to_create', e.target.value.split('\n').filter(Boolean))}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[100px]"
            placeholder="One file path per line"
          />
        ) : (
          <ul className="space-y-1">
            {(spec.files_to_create || []).map((file, i) => (
              <li key={i} className="flex items-center gap-2 text-sm text-gray-300">
                <FilePlus className="w-3.5 h-3.5 text-green-400" />
                <code className="bg-[#21262d] px-1.5 py-0.5 rounded text-xs">{file}</code>
              </li>
            ))}
          </ul>
        )}
      </CollapsibleSection>

      {/* Acceptance Criteria */}
      <CollapsibleSection title="Acceptance Criteria" icon={CheckCircle2}>
        {isEditing ? (
          <textarea
            value={(currentSpec.acceptance_criteria || []).join('\n')}
            onChange={(e) => handleEdit('acceptance_criteria', e.target.value.split('\n').filter(Boolean))}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[150px]"
            placeholder="One criterion per line"
          />
        ) : (
          <ul className="space-y-2">
            {(spec.acceptance_criteria || []).map((criteria, i) => (
              <li key={i} className="flex items-start gap-2 text-sm text-gray-300">
                <CheckCircle2 className="w-4 h-4 text-green-400 flex-shrink-0 mt-0.5" />
                <span>{criteria}</span>
              </li>
            ))}
          </ul>
        )}
      </CollapsibleSection>

      {/* Test Plan */}
      <CollapsibleSection title="Test Plan" icon={FlaskConical}>
        {isEditing ? (
          <textarea
            value={currentSpec.test_plan}
            onChange={(e) => handleEdit('test_plan', e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[120px]"
          />
        ) : (
          <div className="text-sm text-gray-300 whitespace-pre-wrap leading-relaxed">{spec.test_plan}</div>
        )}
      </CollapsibleSection>

      {/* Risk Assessment */}
      <CollapsibleSection title="Risk Assessment" icon={ShieldAlert} defaultOpen={false}>
        {isEditing ? (
          <textarea
            value={currentSpec.risk_assessment}
            onChange={(e) => handleEdit('risk_assessment', e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[100px]"
          />
        ) : (
          <div className="text-sm text-gray-300 whitespace-pre-wrap leading-relaxed">{spec.risk_assessment}</div>
        )}
      </CollapsibleSection>

      {/* Rollback Plan */}
      <CollapsibleSection title="Rollback Plan" icon={RotateCcw} defaultOpen={false}>
        {isEditing ? (
          <textarea
            value={currentSpec.rollback_plan}
            onChange={(e) => handleEdit('rollback_plan', e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded-md p-3 text-gray-300 text-sm min-h-[100px]"
          />
        ) : (
          <div className="text-sm text-gray-300 whitespace-pre-wrap leading-relaxed">{spec.rollback_plan}</div>
        )}
      </CollapsibleSection>

      {/* Required Approvals */}
      {spec.required_approvals && spec.required_approvals.length > 0 && (
        <CollapsibleSection title="Required Approvals" icon={CheckCircle2} defaultOpen={false}>
          <div className="flex flex-wrap gap-2">
            {spec.required_approvals.map((approval, i) => (
              <Badge key={i} variant="outline" className="bg-yellow-500/10 text-yellow-400 border-yellow-500/30 capitalize">
                {approval.replace('_', ' ')}
              </Badge>
            ))}
          </div>
        </CollapsibleSection>
      )}

      {/* Action buttons */}
      {!isEditing && (
        <div className="flex items-center gap-3 pt-4">
          {onApprove && (
            <button
              onClick={onApprove}
              disabled={isApproving}
              className="btn-primary flex items-center gap-2"
            >
              <CheckCircle2 className="w-4 h-4" />
              {isApproving ? 'Approving...' : 'Approve & Start'}
            </button>
          )}
          {onEdit && (
            <button
              onClick={startEditing}
              className="btn-secondary flex items-center gap-2"
            >
              <FileEdit className="w-4 h-4" />
              Edit Spec
            </button>
          )}
          {onReject && (
            <button
              onClick={onReject}
              className="btn-secondary flex items-center gap-2 text-red-400 border-red-500/30 hover:bg-red-500/10"
            >
              <RotateCcw className="w-4 h-4" />
              Reject & Back to Backlog
            </button>
          )}
        </div>
      )}
    </div>
  );
}
