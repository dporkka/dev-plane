'use client';

import React, { useEffect, useRef, useState, useCallback } from 'react';
import type { SSELike } from '@/lib/api';
import type { AgentStep } from '@/lib/types';
import { cn } from '@/lib/utils';
import {
  Wifi,
  WifiOff,
  Loader2,
  CheckCircle,
} from 'lucide-react';

export interface StreamEvent {
  type: 'step' | 'status' | 'cost' | 'error' | 'complete';
  data: any;
}

interface LiveStreamProps {
  runId: string;
  streamFn: (id: string) => SSELike;
  onStepsUpdate: (steps: AgentStep[]) => void;
  onStatusUpdate?: (status: string) => void;
  onCostUpdate?: (cost: number) => void;
  onComplete?: () => void;
}

export function LiveStream({
  runId,
  streamFn,
  onStepsUpdate,
  onStatusUpdate,
  onCostUpdate,
  onComplete,
}: LiveStreamProps) {
  const [connectionStatus, setConnectionStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'complete'>('connecting');
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const eventSourceRef = useRef<SSELike | null>(null);
  const stepsRef = useRef<AgentStep[]>([]);

  const connect = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
    }

    setConnectionStatus('connecting');

    const sse = streamFn(runId);
    eventSourceRef.current = sse;

    sse.onopen = () => {
      setConnectionStatus('connected');
      setReconnectAttempt(0);
    };

    sse.onmessage = (event) => {
      try {
        const parsed: StreamEvent = JSON.parse(event.data);

        switch (parsed.type) {
          case 'step': {
            const newStep = parsed.data as AgentStep;
            stepsRef.current = [...stepsRef.current, newStep];
            onStepsUpdate(stepsRef.current);
            break;
          }
          case 'status': {
            onStatusUpdate?.(parsed.data.status);
            break;
          }
          case 'cost': {
            onCostUpdate?.(parsed.data.total_cost);
            break;
          }
          case 'complete': {
            setConnectionStatus('complete');
            onComplete?.();
            sse.close();
            break;
          }
          case 'error': {
            console.error('SSE error:', parsed.data);
            break;
          }
        }
      } catch (err) {
        console.error('Failed to parse SSE event:', err);
      }
    };

    sse.onerror = () => {
      setConnectionStatus('disconnected');
      sse.close();

      // Auto-reconnect with exponential backoff
      if (reconnectAttempt < 5) {
        const delay = Math.min(1000 * Math.pow(2, reconnectAttempt), 30000);
        setTimeout(() => {
          setReconnectAttempt((prev) => prev + 1);
          connect();
        }, delay);
      }
    };

    return sse;
  }, [runId, streamFn, onStepsUpdate, onStatusUpdate, onCostUpdate, onComplete, reconnectAttempt]);

  useEffect(() => {
    const sse = connect();
    return () => {
      sse.close();
    };
  }, [connect]);

  const statusConfig = {
    connecting: { icon: Loader2, color: 'text-yellow-400', label: 'Connecting...', spin: true },
    connected: { icon: Wifi, color: 'text-green-400', label: 'Live', spin: false },
    disconnected: { icon: WifiOff, color: 'text-red-400', label: 'Disconnected', spin: false },
    complete: { icon: CheckCircle, color: 'text-blue-400', label: 'Complete', spin: false },
  };

  const config = statusConfig[connectionStatus];
  const Icon = config.icon;

  return (
    <div className="flex items-center gap-2 px-3 py-1.5 bg-[#161b22] border border-[#30363d] rounded-md">
      <Icon className={cn('w-3.5 h-3.5', config.color, config.spin && 'animate-spin')} />
      <span className={cn('text-xs', config.color)}>{config.label}</span>
      {connectionStatus === 'disconnected' && reconnectAttempt > 0 && (
        <span className="text-xs text-gray-600">
          Reconnecting ({reconnectAttempt})
        </span>
      )}
      {connectionStatus === 'connected' && (
        <span className="relative flex h-2 w-2">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
          <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
        </span>
      )}
    </div>
  );
}
