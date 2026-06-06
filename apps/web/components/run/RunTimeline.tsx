'use client';

import React, { useState, useRef, useEffect } from 'react';
import { cn } from '@/lib/utils';
import type { AgentRun, AgentStep } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import { CostBadge } from './CostBadge';
import { RunStepDetail } from './RunStepDetail';
import {
  ChevronDown,
  ChevronRight,
  Bot,
  Cpu,
  Zap,
  Brain,
  Wrench,
  Terminal,
  FileDiff,
  AlertCircle,
  MessageSquare,
  CheckCircle,
  Loader2,
  XCircle,
  Clock,
  Play,
  Pause,
  DollarSign,
  Activity,
} from 'lucide-react';

interface RunTimelineProps {
  run: AgentRun;
  steps?: AgentStep[];
  isLive?: boolean;
}

type Phase = 'setup' | 'planning' | 'execution' | 'testing' | 'review' | 'other';

interface GroupedSteps {
  phase: Phase;
  label: string;
  steps: AgentStep[];
}

const roleIcons: Record<string, React.ElementType> = {
  planner: Cpu,
  implementer: Zap,
  reviewer: Bot,
  test_runner: Activity,
  security_reviewer: Bot,
  docs_writer: Bot,
  release_manager: Zap,
};

const typeIcons: Record<string, React.ElementType> = {
  thought: Brain,
  tool_call: Wrench,
  command_run: Terminal,
  file_patch: FileDiff,
  approval_request: AlertCircle,
  message: MessageSquare,
  error: XCircle,
};

const typeColors: Record<string, string> = {
  thought: 'text-purple-400 bg-purple-500/10',
  tool_call: 'text-blue-400 bg-blue-500/10',
  command_run: 'text-yellow-400 bg-yellow-500/10',
  file_patch: 'text-green-400 bg-green-500/10',
  approval_request: 'text-orange-400 bg-orange-500/10',
  message: 'text-gray-400 bg-gray-500/10',
  error: 'text-red-400 bg-red-500/10',
};

const statusIcons: Record<string, React.ElementType> = {
  pending: Clock,
  running: Loader2,
  completed: CheckCircle,
  failed: XCircle,
};

const statusColors: Record<string, string> = {
  pending: 'text-gray-500',
  running: 'text-blue-400',
  completed: 'text-green-400',
  failed: 'text-red-400',
};

function getPhaseForStep(step: AgentStep): Phase {
  const type = step.step_type;
  const content = (step.content || '').toLowerCase();
  const toolName = (step.tool_name || '').toLowerCase();

  if (content.includes('test') || toolName.includes('test')) return 'testing';
  if (content.includes('review') || toolName.includes('review')) return 'review';
  if (content.includes('plan') || type === 'thought') return 'planning';
  if (type === 'command_run' && (content.includes('setup') || content.includes('install') || content.includes('clone'))) return 'setup';
  if (type === 'file_patch' || content.includes('implement') || content.includes('write')) return 'execution';
  return 'other';
}

function groupStepsByPhase(steps: AgentStep[]): GroupedSteps[] {
  const groups: Record<Phase, AgentStep[]> = {
    setup: [],
    planning: [],
    execution: [],
    testing: [],
    review: [],
    other: [],
  };

  steps.forEach((step) => {
    const phase = getPhaseForStep(step);
    groups[phase].push(step);
  });

  const labels: Record<Phase, string> = {
    setup: 'Setup',
    planning: 'Planning',
    execution: 'Execution',
    testing: 'Testing',
    review: 'Review',
    other: 'Other',
  };

  return (Object.keys(groups) as Phase[])
    .filter((phase) => groups[phase].length > 0)
    .map((phase) => ({
      phase,
      label: labels[phase],
      steps: groups[phase],
    }));
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function RunTimeline({ run, steps = [], isLive }: RunTimelineProps) {
  const [expanded, setExpanded] = useState(true);
  const [expandedSteps, setExpandedSteps] = useState<Set<string>>(new Set());
  const timelineEndRef = useRef<HTMLDivElement>(null);

  const Icon = roleIcons[run.agent_role] || Bot;
  const groupedSteps = groupStepsByPhase(steps);
  const totalSteps = steps.length;
  const completedSteps = steps.filter((s) => s.status === 'completed').length;

  // Auto-scroll to bottom when live
  useEffect(() => {
    if (isLive && timelineEndRef.current) {
      timelineEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [steps, isLive]);

  const toggleStep = (id: string) => {
    setExpandedSteps((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  return (
    <Card className="overflow-hidden">
      {/* Run header */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center justify-between p-4 hover:bg-[#21262d]/50 transition-colors"
      >
        <div className="flex items-center gap-3">
          {expanded ? (
            <ChevronDown className="w-4 h-4 text-gray-500" />
          ) : (
            <ChevronRight className="w-4 h-4 text-gray-500" />
          )}
          <div
            className={`p-1.5 rounded-lg ${
              run.status === 'running'
                ? 'bg-blue-500/10'
                : run.status === 'completed'
                ? 'bg-green-500/10'
                : run.status === 'failed'
                ? 'bg-red-500/10'
                : 'bg-gray-800'
            }`}
          >
            <Icon
              className={`w-5 h-5 ${
                run.status === 'running'
                  ? 'text-blue-400'
                  : run.status === 'completed'
                  ? 'text-green-400'
                  : run.status === 'failed'
                  ? 'text-red-400'
                  : 'text-gray-400'
              }`}
            />
          </div>
          <div className="text-left">
            <div className="flex items-center gap-2">
              <span className="font-medium text-white capitalize">
                {run.agent_role.replace('_', ' ')}
              </span>
              <StatusBadge status={run.status} size="sm" />
              {isLive && (
                <span className="relative flex h-2 w-2">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
                </span>
              )}
            </div>
            <div className="text-xs text-gray-500 flex items-center gap-2 mt-0.5">
              {run.model && <span>{run.model}</span>}
              {run.provider && <span className="text-gray-600">via {run.provider}</span>}
              {totalSteps > 0 && (
                <span>
                  {completedSteps}/{totalSteps} steps
                </span>
              )}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-4 text-sm text-gray-500">
          <CostBadge cost={run.total_cost} />
          <span className="flex items-center gap-1">
            <Zap className="w-3 h-3" />
            {(run.prompt_tokens + run.completion_tokens).toLocaleString()} tokens
          </span>
          <TimeAgo date={run.created_at} />
        </div>
      </button>

      {/* Timeline */}
      {expanded && (
        <div className="border-t border-[#30363d]">
          {groupedSteps.length > 0 ? (
            groupedSteps.map((group) => (
              <PhaseGroup
                key={group.phase}
                group={group}
                expandedSteps={expandedSteps}
                onToggleStep={toggleStep}
              />
            ))
          ) : (
            <div className="px-4 py-8 text-center text-gray-600 text-sm">
              {isLive ? (
                <div className="flex items-center justify-center gap-2">
                  <Loader2 className="w-4 h-4 animate-spin" />
                  Waiting for steps...
                </div>
              ) : (
                'No steps recorded'
              )}
            </div>
          )}
          <div ref={timelineEndRef} />
        </div>
      )}
    </Card>
  );
}

function PhaseGroup({
  group,
  expandedSteps,
  onToggleStep,
}: {
  group: GroupedSteps;
  expandedSteps: Set<string>;
  onToggleStep: (id: string) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const phaseColors: Record<Phase, string> = {
    setup: 'border-gray-600',
    planning: 'border-purple-500',
    execution: 'border-blue-500',
    testing: 'border-yellow-500',
    review: 'border-green-500',
    other: 'border-gray-700',
  };

  const phaseIcons: Record<Phase, React.ElementType> = {
    setup: Play,
    planning: Brain,
    execution: Zap,
    testing: Activity,
    review: CheckCircle,
    other: MessageSquare,
  };

  const PhaseIcon = phaseIcons[group.phase];

  return (
    <div className={`border-l-2 ${phaseColors[group.phase]} ml-4 my-2`}>
      {/* Phase header */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 px-3 py-2 text-xs font-medium text-gray-400 hover:text-gray-200 transition-colors"
      >
        {expanded ? (
          <ChevronDown className="w-3 h-3" />
        ) : (
          <ChevronRight className="w-3 h-3" />
        )}
        <PhaseIcon className="w-3.5 h-3.5" />
        <span>{group.label}</span>
        <span className="text-gray-600">({group.steps.length})</span>
      </button>

      {/* Steps */}
      {expanded && (
        <div className="space-y-0.5 ml-2">
          {group.steps.map((step) => (
            <TimelineStep
              key={step.id}
              step={step}
              isExpanded={expandedSteps.has(step.id)}
              onToggle={() => onToggleStep(step.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function TimelineStep({
  step,
  isExpanded,
  onToggle,
}: {
  step: AgentStep;
  isExpanded: boolean;
  onToggle: () => void;
}) {
  const StepIcon = typeIcons[step.step_type] || MessageSquare;
  const typeColor = typeColors[step.step_type] || typeColors.message;
  const StatusIcon = statusIcons[step.status] || statusIcons.pending;
  const statusColor = statusColors[step.status] || statusColors.pending;
  const isRunning = step.status === 'running';

  return (
    <div
      className={cn(
        'rounded-md transition-colors',
        isRunning && 'bg-blue-500/5',
        isExpanded && 'bg-[#161b22]'
      )}
    >
      <button
        onClick={onToggle}
        className="w-full flex items-start gap-3 py-2 px-3 hover:bg-[#21262d]/50 transition-colors text-left"
      >
        {/* Step number */}
        <div className="flex-shrink-0 w-6 text-xs text-gray-600 text-right pt-0.5">
          {step.step_number}
        </div>

        {/* Type icon */}
        <div className={cn('flex-shrink-0 mt-0.5 p-1 rounded', typeColor)}>
          <StepIcon className="w-3.5 h-3.5" />
        </div>

        {/* Content */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-gray-400 capitalize">
              {step.step_type.replace('_', ' ')}
            </span>
            {step.tool_name && (
              <span className="text-[10px] bg-[#21262d] text-gray-400 px-1.5 py-0.5 rounded">
                {step.tool_name}
              </span>
            )}
            {step.file_path && (
              <span className="text-[10px] bg-[#21262d] text-blue-400 px-1.5 py-0.5 rounded truncate max-w-[200px]">
                {step.file_path}
              </span>
            )}
          </div>
          <p className="text-sm text-gray-300 mt-0.5 truncate">
            {step.content || step.command || step.tool_name || '...'}
          </p>
        </div>

        {/* Meta */}
        <div className="flex items-center gap-3 flex-shrink-0">
          {step.cost > 0 && (
            <span className="text-xs text-gray-600">
              ${step.cost.toFixed(4)}
            </span>
          )}
          {step.latency_ms > 0 && (
            <span className="text-xs text-gray-600">
              {formatDuration(step.latency_ms)}
            </span>
          )}
          <div className={cn('flex-shrink-0', statusColor)}>
            <StatusIcon className={cn('w-4 h-4', isRunning && 'animate-spin')} />
          </div>
          {isExpanded ? (
            <ChevronDown className="w-3 h-3 text-gray-500" />
          ) : (
            <ChevronRight className="w-3 h-3 text-gray-500" />
          )}
        </div>
      </button>

      {/* Expanded detail */}
      {isExpanded && (
        <div className="px-3 pb-3 ml-12">
          <RunStepDetail step={step} />
        </div>
      )}
    </div>
  );
}
