'use client';

import React, { useState, useEffect, useRef, useCallback } from 'react';
import { useParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';
import { api, type SSELike } from '@/lib/api';
import type { AgentRun, AgentStep } from '@/lib/types';
import { Card } from '@/components/ui/card';
import { Loading } from '@/components/common/Loading';
import { StatusBadge } from '@/components/common/StatusBadge';
import { CostBadge } from '@/components/run/CostBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import { RunTimeline } from '@/components/run/RunTimeline';
import { Terminal } from '@/components/run/Terminal';
import {
  ArrowLeft,
  Bot,
  Cpu,
  Zap,
  DollarSign,
  Clock,
  Pause,
  Play,
  RotateCcw,
  ScrollText,
  Activity,
  StopCircle,
} from 'lucide-react';
import Link from 'next/link';

const roleIcons: Record<string, React.ElementType> = {
  planner: Cpu,
  implementer: Zap,
  reviewer: Bot,
  test_runner: Activity,
  security_reviewer: Bot,
  docs_writer: Bot,
  release_manager: Zap,
};

function useLiveSteps(runId: string, isRunning: boolean) {
  const [liveSteps, setLiveSteps] = useState<AgentStep[]>([]);
  const [connectionStatus, setConnectionStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'complete'>('connecting');
  const eventSourceRef = useRef<SSELike | null>(null);
  const reconnectCount = useRef(0);

  const connect = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
    }

    setConnectionStatus('connecting');

    const sse = api.streamRun(runId);
    eventSourceRef.current = sse;

    sse.onopen = () => {
      setConnectionStatus('connected');
      reconnectCount.current = 0;
    };

    sse.onmessage = (event) => {
      try {
        const parsed = JSON.parse(event.data);

        switch (parsed.type) {
          case 'step': {
            const newStep = parsed.data as AgentStep;
            setLiveSteps((prev) => {
              const exists = prev.find((s) => s.id === newStep.id);
              if (exists) {
                return prev.map((s) => (s.id === newStep.id ? newStep : s));
              }
              return [...prev, newStep];
            });
            break;
          }
          case 'complete':
            setConnectionStatus('complete');
            sse.close();
            break;
          case 'error':
            console.error('SSE error:', parsed.data);
            break;
        }
      } catch (err) {
        console.error('Failed to parse SSE event:', err);
      }
    };

    sse.onerror = () => {
      setConnectionStatus('disconnected');
      sse.close();
      if (reconnectCount.current < 5) {
        const delay = Math.min(1000 * Math.pow(2, reconnectCount.current), 30000);
        setTimeout(() => {
          reconnectCount.current += 1;
          connect();
        }, delay);
      }
    };

    return sse;
  }, [runId]);

  useEffect(() => {
    if (!isRunning) {
      setConnectionStatus('complete');
      return;
    }

    const sse = connect();
    return () => {
      sse.close();
    };
  }, [connect, isRunning]);

  return { liveSteps, connectionStatus };
}

export default function RunDetailPage() {
  const params = useParams();
  const runId = params.id as string;
  const [elapsedSeconds, setElapsedSeconds] = useState(0);

  const { data: run, isLoading: runLoading } = useQuery<AgentRun>({
    queryKey: ['run', runId],
    queryFn: () => api.getRun(runId),
    enabled: !!runId,
    refetchInterval: (query) =>
      query.state.data?.status === 'running' ? 5000 : false,
  });

  const { data: stepsData } = useQuery({
    queryKey: ['run-steps', runId],
    queryFn: () => api.getRunSteps(runId),
    enabled: !!runId,
  });

  const isRunning = run?.status === 'running';
  const { liveSteps, connectionStatus } = useLiveSteps(runId, isRunning);

  // Merge stored steps with live steps
  const storedSteps: AgentStep[] = React.useMemo(() => {
    const raw = stepsData?.data || stepsData || [];
    return Array.isArray(raw) ? raw : [];
  }, [stepsData]);

  const allSteps: AgentStep[] = React.useMemo(() => {
    if (liveSteps.length === 0) return storedSteps;

    const storedIds = new Set(storedSteps.map((s) => s.id));
    const merged = [...storedSteps];

    liveSteps.forEach((liveStep) => {
      const idx = merged.findIndex((s) => s.id === liveStep.id);
      if (idx >= 0) {
        merged[idx] = liveStep;
      } else {
        merged.push(liveStep);
      }
    });

    return merged.sort((a, b) => a.step_number - b.step_number);
  }, [storedSteps, liveSteps]);

  // Terminal logs from command steps
  const terminalLogs = React.useMemo(() => {
    return allSteps
      .filter((s) => s.command_output || s.command)
      .map((s) => {
        const lines: string[] = [];
        if (s.command) lines.push(`$ ${s.command}`);
        if (s.command_output) lines.push(s.command_output);
        if (s.exit_code !== undefined && s.exit_code !== null) {
          lines.push(`[exit code: ${s.exit_code}]`);
        }
        return lines.join('\n');
      });
  }, [allSteps]);

  // Elapsed timer
  useEffect(() => {
    if (!isRunning) {
      if (run?.started_at && run?.completed_at) {
        const start = new Date(run.started_at).getTime();
        const end = new Date(run.completed_at).getTime();
        setElapsedSeconds(Math.floor((end - start) / 1000));
      }
      return;
    }

    const interval = setInterval(() => {
      if (run?.started_at) {
        const start = new Date(run.started_at).getTime();
        setElapsedSeconds(Math.floor((Date.now() - start) / 1000));
      }
    }, 1000);

    return () => clearInterval(interval);
  }, [isRunning, run?.started_at, run?.completed_at]);

  const handleCancel = async () => {
    try {
      await api.cancelRun(runId);
    } catch (err) {
      console.error('Failed to cancel run:', err);
    }
  };

  if (runLoading) return <Loading />;
  if (!run) {
    return (
      <Card>
        <div className="text-sm text-gray-400">Run not found.</div>
      </Card>
    );
  }

  const Icon = roleIcons[run.agent_role || 'implementer'] || Bot;
  const mins = Math.floor(elapsedSeconds / 60);
  const secs = elapsedSeconds % 60;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link
          href={run?.task_id ? `/tasks/${run.task_id}` : '/dashboard'}
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          {run?.task_id ? 'Back to Task' : 'Back to Dashboard'}
        </Link>

        <div className="flex items-start justify-between flex-wrap gap-4">
          <div className="flex items-center gap-4">
            <div
              className={`p-2 rounded-lg ${
                run?.status === 'running'
                  ? 'bg-blue-500/10'
                  : run?.status === 'completed'
                  ? 'bg-green-500/10'
                  : run?.status === 'failed'
                  ? 'bg-red-500/10'
                  : 'bg-gray-800'
              }`}
            >
              <Icon
                className={`w-6 h-6 ${
                  run?.status === 'running'
                    ? 'text-blue-400'
                    : run?.status === 'completed'
                    ? 'text-green-400'
                    : run?.status === 'failed'
                    ? 'text-red-400'
                    : 'text-gray-400'
                }`}
              />
            </div>
            <div>
              <div className="flex items-center gap-3 mb-1">
                <h1 className="text-2xl font-bold text-white capitalize">
                  {run?.agent_role?.replace('_', ' ') || 'Run'}
                </h1>
                <StatusBadge status={run.status} />
                {connectionStatus === 'connected' && (
                  <span className="text-xs text-green-400 flex items-center gap-1">
                    <span className="relative flex h-2 w-2">
                      <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                      <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
                    </span>
                    Live
                  </span>
                )}
              </div>
              <div className="text-sm text-gray-500 flex items-center gap-3">
                {run?.model && <span>{run.model}</span>}
                {run?.provider && <span>via {run.provider}</span>}
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2">
            {isRunning && (
              <button
                onClick={handleCancel}
                className="btn-secondary flex items-center gap-2 text-red-400 border-red-500/30 hover:bg-red-500/10"
              >
                <StopCircle className="w-4 h-4" />
                Cancel
              </button>
            )}
            {run?.status === 'failed' && (
              <button className="btn-secondary flex items-center gap-2">
                <RotateCcw className="w-4 h-4" />
                Retry
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <DollarSign className="w-3 h-3" />
            Total Cost
          </div>
          <CostBadge cost={run?.total_cost || 0} className="text-white" />
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <Zap className="w-3 h-3" />
            Tokens
          </div>
          <div className="text-white font-medium">
            {((run?.prompt_tokens || 0) + (run?.completion_tokens || 0)).toLocaleString()}
          </div>
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <Clock className="w-3 h-3" />
            Duration
          </div>
          <div className="text-white font-medium">
            {mins}:{secs.toString().padStart(2, '0')}
          </div>
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <ScrollText className="w-3 h-3" />
            Steps
          </div>
          <div className="text-white font-medium">{allSteps.length}</div>
        </Card>
        <Card>
          <div className="flex items-center gap-2 text-gray-500 text-xs mb-1">
            <Activity className="w-3 h-3" />
            Status
          </div>
          <div className="text-white font-medium capitalize">{run?.status}</div>
        </Card>
      </div>

      {/* Connection status */}
      {connectionStatus === 'disconnected' && isRunning && (
        <Card className="p-3 bg-yellow-500/10 border-yellow-500/30">
          <div className="text-sm text-yellow-400 flex items-center gap-2">
            <Clock className="w-4 h-4" />
            Reconnecting to live stream...
          </div>
        </Card>
      )}

      {/* Timeline */}
      {run && (
        <RunTimeline run={run} steps={allSteps} isLive={isRunning && connectionStatus === 'connected'} />
      )}

      {/* Terminal */}
      {terminalLogs.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-white mb-3 flex items-center gap-2">
            <ScrollText className="w-5 h-5 text-gray-400" />
            Command Output
          </h2>
          <Card className="overflow-hidden">
            <Terminal logs={terminalLogs} height="400px" />
          </Card>
        </div>
      )}
    </div>
  );
}
