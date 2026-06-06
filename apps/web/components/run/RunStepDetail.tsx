'use client';

import React from 'react';
import type { AgentStep } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { CodeEditor } from '@/components/code/CodeMirror';
import { cn } from '@/lib/utils';
import {
  Copy,
  CheckCircle,
  XCircle,
  Terminal,
  FileDiff,
  Clock,
  DollarSign,
} from 'lucide-react';

interface RunStepDetailProps {
  step: AgentStep;
}

export function RunStepDetail({ step }: RunStepDetailProps) {
  const [copied, setCopied] = React.useState(false);

  const handleCopy = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const statusColor =
    step.status === 'completed'
      ? 'text-green-400'
      : step.status === 'failed'
      ? 'text-red-400'
      : step.status === 'running'
      ? 'text-blue-400'
      : 'text-gray-500';

  return (
    <div className="space-y-3 py-2">
      {/* Command section */}
      {step.command && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs font-medium text-gray-500 flex items-center gap-1">
              <Terminal className="w-3 h-3" />
              Command
            </span>
            <button
              onClick={() => handleCopy(step.command!)}
              className="text-xs text-gray-500 hover:text-gray-300 flex items-center gap-1"
            >
              {copied ? <CheckCircle className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <div className="bg-[#0d1117] border border-[#30363d] rounded-md p-2">
            <code className="text-xs text-gray-300 font-mono">{step.command}</code>
          </div>
        </div>
      )}

      {/* Exit code badge */}
      {step.exit_code !== undefined && step.exit_code !== null && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-500">Exit code:</span>
          <span
            className={cn(
              'text-xs px-2 py-0.5 rounded font-mono',
              step.exit_code === 0
                ? 'bg-green-500/10 text-green-400'
                : 'bg-red-500/10 text-red-400'
            )}
          >
            {step.exit_code === 0 ? (
              <CheckCircle className="w-3 h-3 inline mr-1" />
            ) : (
              <XCircle className="w-3 h-3 inline mr-1" />
            )}
            {step.exit_code}
          </span>
        </div>
      )}

      {/* Tool input */}
      {step.tool_input && (
        <div>
          <span className="text-xs font-medium text-gray-500 mb-1 block">Input</span>
          <CodeEditor
            value={JSON.stringify(step.tool_input, null, 2)}
            language="json"
            readOnly
            height="120px"
          />
        </div>
      )}

      {/* Tool output */}
      {step.tool_output && (
        <div>
          <span className="text-xs font-medium text-gray-500 mb-1 block">Output</span>
          <CodeEditor
            value={
              typeof step.tool_output === 'string'
                ? step.tool_output
                : JSON.stringify(step.tool_output, null, 2)
            }
            language="json"
            readOnly
            height="120px"
          />
        </div>
      )}

      {/* Command output */}
      {step.command_output && (
        <div>
          <span className="text-xs font-medium text-gray-500 mb-1 block">Output</span>
          <div className="bg-[#0d1117] border border-[#30363d] rounded-md p-3 max-h-60 overflow-auto">
            <pre className="text-xs text-gray-300 whitespace-pre-wrap">{step.command_output}</pre>
          </div>
        </div>
      )}

      {/* File diff */}
      {step.diff && (
        <div>
          <span className="text-xs font-medium text-gray-500 mb-1 flex items-center gap-1">
            <FileDiff className="w-3 h-3" />
            Diff
          </span>
          <CodeEditor value={step.diff} language="typescript" readOnly height="200px" />
        </div>
      )}

      {/* Metrics */}
      <div className="flex items-center gap-4 text-xs text-gray-600">
        {step.cost > 0 && (
          <span className="flex items-center gap-1">
            <DollarSign className="w-3 h-3" />
            ${step.cost.toFixed(4)}
          </span>
        )}
        {step.latency_ms > 0 && (
          <span className="flex items-center gap-1">
            <Clock className="w-3 h-3" />
            {(step.latency_ms / 1000).toFixed(1)}s
          </span>
        )}
        <span className={statusColor}>
          {step.status}
        </span>
      </div>
    </div>
  );
}
