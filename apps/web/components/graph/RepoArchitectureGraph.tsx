'use client';

import { useCallback, useMemo, useState } from 'react';
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  Panel,
  useNodesState,
  useEdgesState,
  Handle,
  Position,
  type Node,
  type Edge,
  type ConnectionMode,
} from 'reactflow';
import 'reactflow/dist/style.css';
import {
  Folder,
  FolderOpen,
  FileCode,
  FileJson,
  FileText,
  FileType,
  Search,
  ZoomIn,
  ZoomOut,
  Maximize,
} from 'lucide-react';

// File type colors for different extensions
const fileTypeColors: Record<string, string> = {
  ts: 'border-blue-500/40 bg-blue-500/10 text-blue-400',
  tsx: 'border-cyan-500/40 bg-cyan-500/10 text-cyan-400',
  go: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-400',
  css: 'border-pink-500/40 bg-pink-500/10 text-pink-400',
  json: 'border-yellow-500/40 bg-yellow-500/10 text-yellow-400',
  md: 'border-gray-500/40 bg-gray-500/10 text-gray-400',
  sql: 'border-orange-500/40 bg-orange-500/10 text-orange-400',
  yaml: 'border-violet-500/40 bg-violet-500/10 text-violet-400',
  yml: 'border-violet-500/40 bg-violet-500/10 text-violet-400',
  default: 'border-gray-600/40 bg-gray-600/10 text-gray-400',
};

function getFileColor(filename: string): string {
  const ext = filename.split('.').pop()?.toLowerCase() || 'default';
  return fileTypeColors[ext] || fileTypeColors.default;
}

function getFileIcon(filename: string) {
  const ext = filename.split('.').pop()?.toLowerCase();
  switch (ext) {
    case 'ts':
    case 'tsx':
    case 'js':
    case 'jsx':
      return <FileCode size={14} />;
    case 'json':
      return <FileJson size={14} />;
    case 'md':
    case 'txt':
      return <FileText size={14} />;
    default:
      return <FileType size={14} />;
  }
}

interface FileNodeData {
  label: string;
  type: 'directory' | 'file';
  path: string;
  children?: number;
  imports?: string[];
}

// Custom node component for file/directory nodes
function FileNode({ data }: { data: FileNodeData }) {
  if (data.type === 'directory') {
    return (
      <div className="rounded-md border border-gray-600/50 bg-gray-800/80 px-3 py-2 min-w-[160px] backdrop-blur-sm hover:border-blue-500/50 transition-colors cursor-pointer group">
        <Handle type="target" position={Position.Top} className="!bg-gray-500 !w-2 !h-2" />
        <div className="flex items-center gap-2">
          <FolderOpen size={16} className="text-blue-400 group-hover:text-blue-300" />
          <span className="text-xs font-medium text-gray-200">{data.label}</span>
          {data.children && (
            <span className="text-[10px] text-gray-500 ml-auto">{data.children}</span>
          )}
        </div>
        <Handle type="source" position={Position.Bottom} className="!bg-gray-500 !w-2 !h-2" />
      </div>
    );
  }

  const colorClass = getFileColor(data.label);

  return (
    <div
      className={`rounded-md border ${colorClass} px-3 py-1.5 min-w-[140px] backdrop-blur-sm hover:brightness-125 transition-all cursor-pointer`}
    >
      <Handle type="target" position={Position.Top} className="!bg-gray-500 !w-2 !h-2" />
      <div className="flex items-center gap-2">
        {getFileIcon(data.label)}
        <span className="text-xs font-medium">{data.label}</span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-gray-500 !w-2 !h-2" />
    </div>
  );
}

const nodeTypes = {
  file: FileNode,
};

// Generate demo repository structure
function generateDemoNodes(): Node<FileNodeData>[] {
  const nodes: Node<FileNodeData>[] = [
    // Root
    {
      id: 'root',
      type: 'file',
      position: { x: 400, y: 0 },
      data: { label: 'ai-dev-control-plane', type: 'directory', path: '/', children: 4 },
    },

    // Apps
    {
      id: 'apps',
      type: 'file',
      position: { x: 100, y: 100 },
      data: { label: 'apps', type: 'directory', path: '/apps', children: 2 },
    },
    {
      id: 'apps-web',
      type: 'file',
      position: { x: 50, y: 200 },
      data: {
        label: 'web',
        type: 'directory',
        path: '/apps/web',
        children: 4,
        imports: ['packages/agents', 'packages/models'],
      },
    },
    {
      id: 'apps-api',
      type: 'file',
      position: { x: 200, y: 200 },
      data: {
        label: 'api',
        type: 'directory',
        path: '/apps/api',
        children: 3,
        imports: ['packages/db', 'packages/agents', 'packages/events'],
      },
    },

    // Web app files
    {
      id: 'web-layout',
      type: 'file',
      position: { x: 0, y: 310 },
      data: { label: 'layout.tsx', type: 'file', path: '/apps/web/app/layout.tsx' },
    },
    {
      id: 'web-page',
      type: 'file',
      position: { x: 100, y: 310 },
      data: { label: 'page.tsx', type: 'file', path: '/apps/web/app/page.tsx' },
    },
    {
      id: 'web-globals',
      type: 'file',
      position: { x: 200, y: 310 },
      data: { label: 'globals.css', type: 'file', path: '/apps/web/app/globals.css' },
    },

    // API files
    {
      id: 'api-main',
      type: 'file',
      position: { x: 250, y: 310 },
      data: { label: 'main.go', type: 'file', path: '/apps/api/cmd/api/main.go' },
    },

    // Packages
    {
      id: 'packages',
      type: 'file',
      position: { x: 500, y: 100 },
      data: { label: 'packages', type: 'directory', path: '/packages', children: 8 },
    },
    {
      id: 'pkg-agents',
      type: 'file',
      position: { x: 350, y: 200 },
      data: {
        label: 'agents',
        type: 'directory',
        path: '/packages/agents',
        children: 3,
        imports: ['packages/models'],
      },
    },
    {
      id: 'pkg-db',
      type: 'file',
      position: { x: 500, y: 200 },
      data: {
        label: 'db',
        type: 'directory',
        path: '/packages/db',
        children: 4,
        imports: ['packages/models'],
      },
    },
    {
      id: 'pkg-events',
      type: 'file',
      position: { x: 650, y: 200 },
      data: { label: 'events', type: 'directory', path: '/packages/events', children: 2 },
    },
    {
      id: 'pkg-runtimes',
      type: 'file',
      position: { x: 800, y: 200 },
      data: {
        label: 'runtimes',
        type: 'directory',
        path: '/packages/runtimes',
        children: 3,
      },
    },
    {
      id: 'pkg-models',
      type: 'file',
      position: { x: 950, y: 200 },
      data: {
        label: 'models',
        type: 'directory',
        path: '/packages/models',
        children: 8,
      },
    },

    // Package files
    {
      id: 'agents-roles',
      type: 'file',
      position: { x: 300, y: 310 },
      data: { label: 'roles.go', type: 'file', path: '/packages/agents/roles.go' },
    },
    {
      id: 'agents-tools',
      type: 'file',
      position: { x: 400, y: 310 },
      data: { label: 'tools.go', type: 'file', path: '/packages/agents/tools.go' },
    },
    {
      id: 'db-schema',
      type: 'file',
      position: { x: 550, y: 310 },
      data: { label: 'schema.sql', type: 'file', path: '/packages/db/schema.sql' },
    },
    {
      id: 'runtime-docker',
      type: 'file',
      position: { x: 750, y: 310 },
      data: { label: 'docker.go', type: 'file', path: '/packages/runtimes/docker.go' },
    },
    {
      id: 'runtime-local',
      type: 'file',
      position: { x: 850, y: 310 },
      data: { label: 'local.go', type: 'file', path: '/packages/runtimes/local.go' },
    },

    // Docs
    {
      id: 'docs',
      type: 'file',
      position: { x: 750, y: 100 },
      data: { label: 'docs', type: 'directory', path: '/docs', children: 5 },
    },
  ];

  return nodes;
}

function generateDemoEdges(): Edge[] {
  return [
    // Directory structure edges
    { id: 'e-root-apps', source: 'root', target: 'apps', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-root-packages', source: 'root', target: 'packages', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-root-docs', source: 'root', target: 'docs', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-apps-web', source: 'apps', target: 'apps-web', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-apps-api', source: 'apps', target: 'apps-api', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-pkg-agents', source: 'packages', target: 'pkg-agents', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-pkg-db', source: 'packages', target: 'pkg-db', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-pkg-events', source: 'packages', target: 'pkg-events', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-pkg-runtimes', source: 'packages', target: 'pkg-runtimes', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-pkg-models', source: 'packages', target: 'pkg-models', style: { stroke: '#4b5563', strokeWidth: 1.5 } },
    { id: 'e-web-layout', source: 'apps-web', target: 'web-layout', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-web-page', source: 'apps-web', target: 'web-page', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-web-globals', source: 'apps-web', target: 'web-globals', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-api-main', source: 'apps-api', target: 'api-main', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-agents-roles', source: 'pkg-agents', target: 'agents-roles', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-agents-tools', source: 'pkg-agents', target: 'agents-tools', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-db-schema', source: 'pkg-db', target: 'db-schema', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-runtime-docker', source: 'pkg-runtimes', target: 'runtime-docker', style: { stroke: '#374151', strokeWidth: 1 } },
    { id: 'e-runtime-local', source: 'pkg-runtimes', target: 'runtime-local', style: { stroke: '#374151', strokeWidth: 1 } },

    // Import dependency edges (dashed, colored)
    {
      id: 'e-web-import-agents',
      source: 'apps-web',
      target: 'pkg-agents',
      style: { stroke: '#8b5cf6', strokeWidth: 1.5, strokeDasharray: '5,5' },
      label: 'imports',
      labelStyle: { fill: '#8b949e', fontSize: 9 },
    },
    {
      id: 'e-api-import-agents',
      source: 'apps-api',
      target: 'pkg-agents',
      style: { stroke: '#8b5cf6', strokeWidth: 1.5, strokeDasharray: '5,5' },
      label: 'imports',
      labelStyle: { fill: '#8b949e', fontSize: 9 },
    },
    {
      id: 'e-api-import-db',
      source: 'apps-api',
      target: 'pkg-db',
      style: { stroke: '#8b5cf6', strokeWidth: 1.5, strokeDasharray: '5,5' },
      label: 'imports',
      labelStyle: { fill: '#8b949e', fontSize: 9 },
    },
    {
      id: 'e-agents-import-models',
      source: 'pkg-agents',
      target: 'pkg-models',
      style: { stroke: '#f59e0b', strokeWidth: 1.5, strokeDasharray: '5,5' },
      label: 'uses',
      labelStyle: { fill: '#8b949e', fontSize: 9 },
    },
    {
      id: 'e-db-import-models',
      source: 'pkg-db',
      target: 'pkg-models',
      style: { stroke: '#f59e0b', strokeWidth: 1.5, strokeDasharray: '5,5' },
      label: 'uses',
      labelStyle: { fill: '#8b949e', fontSize: 9 },
    },
  ];
}

interface RepoArchitectureGraphProps {
  className?: string;
  onNodeClick?: (path: string) => void;
}

export function RepoArchitectureGraph({
  className = '',
  onNodeClick,
}: RepoArchitectureGraphProps) {
  const initialNodes = useMemo(() => generateDemoNodes(), []);
  const initialEdges = useMemo(() => generateDemoEdges(), []);
  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges);
  const [searchTerm, setSearchTerm] = useState('');

  const handleNodeClick = useCallback(
    (_: React.MouseEvent, node: Node<FileNodeData>) => {
      if (node.data.type === 'file' && onNodeClick) {
        onNodeClick(node.data.path);
      }
    },
    [onNodeClick]
  );

  // Filter nodes based on search
  const filteredNodes = useMemo(() => {
    if (!searchTerm) return nodes;
    const term = searchTerm.toLowerCase();
    return nodes.map((node) => ({
      ...node,
      hidden: !node.data.label.toLowerCase().includes(term),
    }));
  }, [nodes, searchTerm]);

  const proOptions = useMemo(
    () => ({
      hideAttribution: true,
    }),
    []
  );

  return (
    <div className={`w-full h-full ${className}`}>
      <ReactFlow
        nodes={filteredNodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={handleNodeClick}
        nodeTypes={nodeTypes}
        connectionMode={'loose' as ConnectionMode}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        proOptions={proOptions}
      >
        <Background color="#30363d" gap={20} size={1} />
        <Controls className="bg-gray-800 border-gray-700 text-gray-300" />
        <MiniMap
          nodeColor={(node) => {
            if (node.data?.type === 'directory') return '#3b82f6';
            return '#6b7280';
          }}
          maskColor="rgba(13, 17, 23, 0.7)"
          className="bg-gray-900 border border-gray-700 rounded-lg"
        />

        {/* Search Panel */}
        <Panel position="top-left">
          <div className="bg-gray-900/90 border border-gray-700 rounded-lg p-3 backdrop-blur-sm space-y-3">
            <div className="relative">
              <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-500" />
              <input
                type="text"
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
                placeholder="Search files..."
                className="w-48 pl-8 pr-3 py-1.5 bg-gray-800 border border-gray-700 rounded-md text-xs text-gray-200 placeholder-gray-500 focus:outline-none focus:border-blue-500"
              />
            </div>
            <div className="space-y-1">
              <div className="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Legend</div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <Folder size={12} className="text-blue-400" />
                Directory
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <FileCode size={12} className="text-gray-400" />
                File
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <div className="w-6 h-0.5 border-t border-dashed border-violet-500" />
                Import
              </div>
            </div>
          </div>
        </Panel>

        {/* File type legend */}
        <Panel position="bottom-left">
          <div className="bg-gray-900/90 border border-gray-700 rounded-lg p-3 backdrop-blur-sm">
            <div className="text-[10px] font-medium text-gray-500 uppercase tracking-wider mb-2">File Types</div>
            <div className="grid grid-cols-3 gap-x-4 gap-y-1">
              {Object.entries({
                '.ts/.tsx': 'border-blue-500/40 text-blue-400',
                '.go': 'border-emerald-500/40 text-emerald-400',
                '.css': 'border-pink-500/40 text-pink-400',
                '.json': 'border-yellow-500/40 text-yellow-400',
                '.sql': 'border-orange-500/40 text-orange-400',
                '.md': 'border-gray-500/40 text-gray-400',
              }).map(([ext, color]) => (
                <div key={ext} className="flex items-center gap-1.5 text-[10px]">
                  <div className={`w-2 h-2 rounded-sm border ${color}`} />
                  <span className="text-gray-400">{ext}</span>
                </div>
              ))}
            </div>
          </div>
        </Panel>
      </ReactFlow>
    </div>
  );
}
