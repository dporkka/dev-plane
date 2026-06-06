'use client';

import React, { useState } from 'react';
import { cn } from '@/lib/utils';
import {
  ChevronRight,
  ChevronDown,
  FileCode,
  Folder,
  FolderOpen,
  FileText,
  FileJson,
  File,
} from 'lucide-react';

export interface FileNode {
  name: string;
  type: 'file' | 'directory';
  path: string;
  children?: FileNode[];
  language?: string;
}

interface FileTreeProps {
  nodes: FileNode[];
  onSelect?: (node: FileNode) => void;
  selectedPath?: string;
  className?: string;
}

function getFileIcon(filename: string) {
  if (filename.endsWith('.ts') || filename.endsWith('.tsx')) return FileCode;
  if (filename.endsWith('.js') || filename.endsWith('.jsx')) return FileCode;
  if (filename.endsWith('.json')) return FileJson;
  if (filename.endsWith('.md')) return FileText;
  return File;
}

function FileTreeNode({
  node,
  depth,
  onSelect,
  selectedPath,
}: {
  node: FileNode;
  depth: number;
  onSelect?: (node: FileNode) => void;
  selectedPath?: string;
}) {
  const [expanded, setExpanded] = useState(depth < 2);
  const isSelected = selectedPath === node.path;

  if (node.type === 'directory') {
    return (
      <div>
        <button
          onClick={() => setExpanded(!expanded)}
          className={cn(
            'flex items-center gap-1 w-full text-left py-1 px-2 rounded-md transition-colors',
            'hover:bg-[#21262d] text-gray-300',
            isSelected && 'bg-blue-500/10 text-blue-400'
          )}
          style={{ paddingLeft: `${depth * 12 + 8}px` }}
        >
          {expanded ? (
            <ChevronDown className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
          ) : (
            <ChevronRight className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
          )}
          {expanded ? (
            <FolderOpen className="w-4 h-4 text-blue-400 flex-shrink-0" />
          ) : (
            <Folder className="w-4 h-4 text-blue-400 flex-shrink-0" />
          )}
          <span className="text-sm truncate">{node.name}</span>
        </button>
        {expanded && node.children && (
          <div>
            {node.children.map((child) => (
              <FileTreeNode
                key={child.path}
                node={child}
                depth={depth + 1}
                onSelect={onSelect}
                selectedPath={selectedPath}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  const Icon = getFileIcon(node.name);

  return (
    <button
      onClick={() => onSelect?.(node)}
      className={cn(
        'flex items-center gap-1.5 w-full text-left py-1 px-2 rounded-md transition-colors',
        'hover:bg-[#21262d] text-gray-400 hover:text-gray-200',
        isSelected && 'bg-blue-500/10 text-blue-400'
      )}
      style={{ paddingLeft: `${depth * 12 + 24}px` }}
    >
      <Icon className="w-4 h-4 flex-shrink-0" />
      <span className="text-sm truncate">{node.name}</span>
    </button>
  );
}

export function FileTree({ nodes, onSelect, selectedPath, className }: FileTreeProps) {
  return (
    <div className={cn('space-y-0.5', className)}>
      {nodes.map((node) => (
        <FileTreeNode
          key={node.path}
          node={node}
          depth={0}
          onSelect={onSelect}
          selectedPath={selectedPath}
        />
      ))}
    </div>
  );
}
