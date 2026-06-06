'use client';

import { useCallback, useMemo } from 'react';
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  Panel,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type ConnectionMode,
} from 'reactflow';
import 'reactflow/dist/style.css';
import {
  Globe,
  Server,
  Cpu,
  Container,
  Database,
  Radio,
  Workflow,
  Shield,
  HardDrive,
} from 'lucide-react';

interface ServiceNodeData {
  label: string;
  icon: React.ReactNode;
  description: string;
  status: 'active' | 'inactive' | 'optional' | 'planned';
  details?: string[];
}

// Custom node component for service nodes
function ServiceNode({ data }: { data: ServiceNodeData }) {
  const statusColors = {
    active: 'border-emerald-500/50 bg-emerald-500/5',
    inactive: 'border-red-500/30 bg-red-500/5',
    optional: 'border-amber-500/30 bg-amber-500/5',
    planned: 'border-sky-500/30 bg-sky-500/5',
  };

  const statusDot = {
    active: 'bg-emerald-500',
    inactive: 'bg-red-500',
    optional: 'bg-amber-500',
    planned: 'bg-sky-500',
  };

  return (
    <div
      className={`rounded-lg border ${statusColors[data.status]} p-4 min-w-[180px] backdrop-blur-sm`}
    >
      <div className="flex items-center gap-3 mb-2">
        <div className="text-blue-400">{data.icon}</div>
        <span className="font-semibold text-sm text-gray-100">{data.label}</span>
        <div className={`w-2 h-2 rounded-full ${statusDot[data.status]} ml-auto`} />
      </div>
      <p className="text-xs text-gray-400 mb-2">{data.description}</p>
      {data.details && (
        <div className="flex flex-wrap gap-1">
          {data.details.map((d, i) => (
            <span
              key={i}
              className="text-[10px] px-1.5 py-0.5 rounded bg-gray-800 text-gray-400 border border-gray-700"
            >
              {d}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

const nodeTypes = {
  service: ServiceNode,
};

const defaultNodes: Node<ServiceNodeData>[] = [
  // Client Layer
  {
    id: 'web-ui',
    type: 'service',
    position: { x: 400, y: 0 },
    data: {
      label: 'Web UI',
      icon: <Globe size={18} />,
      description: 'Next.js 16 + TypeScript frontend',
      status: 'active',
      details: ['React 19', 'Tailwind CSS', 'shadcn/ui'],
    },
  },

  // API Layer
  {
    id: 'api-server',
    type: 'service',
    position: { x: 400, y: 140 },
    data: {
      label: 'API Server',
      icon: <Server size={18} />,
      description: 'Go 1.23 + Chi Router REST API',
      status: 'active',
      details: ['Auth/AuthZ', 'REST', 'WebSocket'],
    },
  },

  // Worker Layer
  {
    id: 'worker',
    type: 'service',
    position: { x: 200, y: 280 },
    data: {
      label: 'Worker',
      icon: <Cpu size={18} />,
      description: 'Task consumer + agent orchestrator',
      status: 'active',
      details: ['Task Queue', 'Agent Dispatch', 'Step Runner'],
    },
  },
  {
    id: 'agent-runner',
    type: 'service',
    position: { x: 600, y: 280 },
    data: {
      label: 'Agent Runner',
      icon: <Shield size={18} />,
      description: 'Agent execution engine with tool access',
      status: 'active',
      details: ['7 Roles', '10 Tools', 'Token Tracking'],
    },
  },

  // Runtime Layer
  {
    id: 'docker-runtime',
    type: 'service',
    position: { x: 600, y: 420 },
    data: {
      label: 'Runtime Provider',
      icon: <Container size={18} />,
      description: 'Workspace execution provider',
      status: 'active',
      details: ['Local + Docker', 'No Network', 'Kernel Gated'],
    },
  },

  // Infrastructure Layer
  {
    id: 'nats',
    type: 'service',
    position: { x: 0, y: 200 },
    data: {
      label: 'NATS JetStream',
      icon: <Radio size={18} />,
      description: 'Event streaming + message bus',
      status: 'active',
      details: ['Pub/Sub', 'Work Queues', 'Persistence'],
    },
  },
  {
    id: 'temporal',
    type: 'service',
    position: { x: 0, y: 320 },
    data: {
      label: 'Temporal',
      icon: <Workflow size={18} />,
      description: 'Workflow orchestration engine',
      status: 'optional',
      details: ['Retries', 'Sagas', 'Scheduling'],
    },
  },
  {
    id: 'database',
    type: 'service',
    position: { x: 0, y: 440 },
    data: {
      label: 'Database',
      icon: <Database size={18} />,
      description: 'SQLite local / Postgres cloud',
      status: 'active',
      details: ['13 Tables', 'SQLC', 'Goose Migrations'],
    },
  },

  // Storage
  {
    id: 'storage',
    type: 'service',
    position: { x: 800, y: 440 },
    data: {
      label: 'File Storage',
      icon: <HardDrive size={18} />,
      description: 'Workspace volumes + git repos',
      status: 'active',
      details: ['Docker Volumes', 'Git Worktrees'],
    },
  },
];

const defaultEdges: Edge[] = [
  // Web UI -> API
  {
    id: 'e-web-api',
    source: 'web-ui',
    target: 'api-server',
    animated: true,
    style: { stroke: '#3b82f6', strokeWidth: 2 },
    label: 'HTTPS/WS',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // API -> Worker
  {
    id: 'e-api-worker',
    source: 'api-server',
    target: 'worker',
    animated: true,
    style: { stroke: '#3b82f6', strokeWidth: 2 },
    label: 'gRPC',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // Worker -> Agent Runner
  {
    id: 'e-worker-runner',
    source: 'worker',
    target: 'agent-runner',
    animated: true,
    style: { stroke: '#3b82f6', strokeWidth: 2 },
    label: 'Dispatch',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // Agent Runner -> Docker
  {
    id: 'e-runner-docker',
    source: 'agent-runner',
    target: 'docker-runtime',
    animated: true,
    style: { stroke: '#10b981', strokeWidth: 2 },
    label: 'Exec',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // API -> NATS
  {
    id: 'e-api-nats',
    source: 'api-server',
    target: 'nats',
    style: { stroke: '#8b5cf6', strokeWidth: 1.5, strokeDasharray: '5,5' },
    label: 'Events',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // Worker -> NATS
  {
    id: 'e-worker-nats',
    source: 'worker',
    target: 'nats',
    style: { stroke: '#8b5cf6', strokeWidth: 1.5, strokeDasharray: '5,5' },
    label: 'Consume',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // NATS -> Temporal
  {
    id: 'e-nats-temporal',
    source: 'nats',
    target: 'temporal',
    style: { stroke: '#f59e0b', strokeWidth: 1.5, strokeDasharray: '5,5' },
    label: 'Workflows',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // API -> Database
  {
    id: 'e-api-db',
    source: 'api-server',
    target: 'database',
    style: { stroke: '#6b7280', strokeWidth: 1.5 },
    label: 'SQL',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // Worker -> Database
  {
    id: 'e-worker-db',
    source: 'worker',
    target: 'database',
    style: { stroke: '#6b7280', strokeWidth: 1.5 },
    label: 'SQL',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },

  // Docker -> Storage
  {
    id: 'e-docker-storage',
    source: 'docker-runtime',
    target: 'storage',
    style: { stroke: '#6b7280', strokeWidth: 1.5 },
    label: 'Volumes',
    labelStyle: { fill: '#8b949e', fontSize: 11 },
  },
];

interface SystemArchitectureGraphProps {
  className?: string;
  showMinimap?: boolean;
}

export function SystemArchitectureGraph({
  className = '',
  showMinimap = true,
}: SystemArchitectureGraphProps) {
  const [nodes, setNodes, onNodesChange] = useNodesState(defaultNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(defaultEdges);

  const onNodeClick = useCallback((_: React.MouseEvent, node: Node) => {
    // Could navigate to service detail page or show a panel
    console.log('Clicked node:', node.id);
  }, []);

  const proOptions = useMemo(
    () => ({
      hideAttribution: true,
    }),
    []
  );

  return (
    <div className={`w-full h-full ${className}`}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={onNodeClick}
        nodeTypes={nodeTypes}
        connectionMode={'loose' as ConnectionMode}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        proOptions={proOptions}
      >
        <Background color="#30363d" gap={20} size={1} />
        <Controls className="bg-gray-800 border-gray-700 text-gray-300" />
        {showMinimap && (
          <MiniMap
            nodeColor={(node) => {
              switch (node.data?.status) {
                case 'active':
                  return '#10b981';
                case 'inactive':
                  return '#ef4444';
                case 'optional':
                  return '#f59e0b';
                case 'planned':
                  return '#38bdf8';
                default:
                  return '#6b7280';
              }
            }}
            maskColor="rgba(13, 17, 23, 0.7)"
            className="bg-gray-900 border border-gray-700 rounded-lg"
          />
        )}
        <Panel position="top-left">
          <div className="bg-gray-900/90 border border-gray-700 rounded-lg p-3 backdrop-blur-sm">
            <h3 className="text-sm font-semibold text-gray-200 mb-2">Service Status</h3>
            <div className="space-y-1.5">
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-2 h-2 rounded-full bg-emerald-500" />
                Active
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-2 h-2 rounded-full bg-amber-500" />
                Optional
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-2 h-2 rounded-full bg-red-500" />
                Inactive
              </div>
            </div>
          </div>
        </Panel>
        <Panel position="bottom-right">
          <div className="bg-gray-900/90 border border-gray-700 rounded-lg p-3 backdrop-blur-sm">
            <div className="space-y-1.5">
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-8 h-0.5 bg-blue-500 rounded" />
                API Flow
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-8 h-0.5 bg-emerald-500 rounded" />
                Execution
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-8 h-0.5 border-t border-dashed border-violet-500" />
                Events
              </div>
            </div>
          </div>
        </Panel>
      </ReactFlow>
    </div>
  );
}
