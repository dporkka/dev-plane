'use client';

import React, { useState, useMemo } from 'react';
import type { FileNode } from '@/components/code/FileTree';
import { FileTree } from '@/components/code/FileTree';
import { Input } from '@/components/ui/input';
import {
  Search,
  X,
  FileDiff,
  FilePlus,
  CircleDot,
} from 'lucide-react';

export interface FileWithStatus extends FileNode {
  gitStatus?: 'modified' | 'added' | 'untracked' | 'deleted';
}

interface FileBrowserProps {
  files: FileWithStatus[];
  selectedPath?: string;
  onSelectFile: (node: FileWithStatus) => void;
  onRequestDiff?: (path: string) => void;
  isLoading?: boolean;
}

function GitStatusIcon({ status }: { status?: string }) {
  switch (status) {
    case 'modified':
      return <FileDiff className="w-3 h-3 text-yellow-400 ml-1" />;
    case 'added':
      return <FilePlus className="w-3 h-3 text-green-400 ml-1" />;
    case 'untracked':
      return <CircleDot className="w-3 h-3 text-gray-500 ml-1" />;
    default:
      return null;
  }
}

function FileTreeWithStatus({
  nodes,
  onSelect,
  selectedPath,
}: {
  nodes: FileWithStatus[];
  onSelect: (node: FileWithStatus) => void;
  selectedPath?: string;
}) {
  return <FileTree nodes={nodes} onSelect={onSelect as any} selectedPath={selectedPath} />;
}

export function FileBrowser({
  files,
  selectedPath,
  onSelectFile,
  onRequestDiff,
  isLoading,
}: FileBrowserProps) {
  const [search, setSearch] = useState('');
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    node: FileWithStatus;
  } | null>(null);

  const filteredFiles = useMemo(() => {
    if (!search.trim()) return files;
    const term = search.toLowerCase();

    function filterNodes(nodes: FileWithStatus[]): FileWithStatus[] {
      return nodes
        .map((node) => {
          if (node.type === 'directory') {
            const filteredChildren = node.children ? filterNodes(node.children as FileWithStatus[]) : [];
            const matches = node.name.toLowerCase().includes(term);
            if (matches || filteredChildren.length > 0) {
              return { ...node, children: filteredChildren };
            }
            return null;
          }
          return node.name.toLowerCase().includes(term) ? node : null;
        })
        .filter(Boolean) as FileWithStatus[];
    }

    return filterNodes(files);
  }, [files, search]);

  const handleContextMenu = (e: React.MouseEvent, node: FileWithStatus) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, node });
  };

  const handleCloseContextMenu = () => setContextMenu(null);

  React.useEffect(() => {
    document.addEventListener('click', handleCloseContextMenu);
    return () => document.removeEventListener('click', handleCloseContextMenu);
  }, []);

  if (isLoading) {
    return (
      <div className="h-full bg-[#0d1117] flex flex-col">
        <div className="p-2 border-b border-[#30363d]">
          <div className="h-8 bg-[#21262d] rounded animate-pulse" />
        </div>
        <div className="flex-1 p-2 space-y-1">
          {Array.from({ length: 10 }).map((_, i) => (
            <div key={i} className="h-6 bg-[#21262d] rounded animate-pulse" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="h-full bg-[#0d1117] flex flex-col" onContextMenu={handleCloseContextMenu}>
      {/* Search */}
      <div className="p-2 border-b border-[#30363d]">
        <div className="relative">
          <Search className="w-3.5 h-3.5 text-gray-500 absolute left-2 top-1/2 -translate-y-1/2" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search files..."
            className="pl-7 pr-7 h-8 text-xs bg-[#21262d] border-[#30363d]"
          />
          {search && (
            <button
              onClick={() => setSearch('')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>
      </div>

      {/* File tree */}
      <div className="flex-1 overflow-auto p-2" onContextMenu={(e) => e.preventDefault()}>
        <FileTreeWithStatus
          nodes={filteredFiles}
          onSelect={(node) => {
            onSelectFile(node);
          }}
          selectedPath={selectedPath}
        />
      </div>

      {/* Context menu */}
      {contextMenu && (
        <div
          className="fixed z-50 bg-[#161b22] border border-[#30363d] rounded-md shadow-lg py-1 min-w-[160px]"
          style={{ top: contextMenu.y, left: contextMenu.x }}
        >
          <button
            className="w-full text-left px-3 py-1.5 text-sm text-gray-300 hover:bg-[#21262d] flex items-center gap-2"
            onClick={() => {
              onSelectFile(contextMenu.node);
              setContextMenu(null);
            }}
          >
            <Search className="w-3.5 h-3.5" />
            Open
          </button>
          <button
            className="w-full text-left px-3 py-1.5 text-sm text-gray-300 hover:bg-[#21262d] flex items-center gap-2"
            onClick={() => {
              navigator.clipboard.writeText(contextMenu.node.path);
              setContextMenu(null);
            }}
          >
            <CircleDot className="w-3.5 h-3.5" />
            Copy Path
          </button>
          {onRequestDiff && contextMenu.node.type === 'file' && (
            <button
              className="w-full text-left px-3 py-1.5 text-sm text-gray-300 hover:bg-[#21262d] flex items-center gap-2"
              onClick={() => {
                onRequestDiff(contextMenu.node.path);
                setContextMenu(null);
              }}
            >
              <FileDiff className="w-3.5 h-3.5" />
              Git Diff
            </button>
          )}
        </div>
      )}

      {/* Git status legend */}
      <div className="p-2 border-t border-[#30363d] text-[10px] text-gray-600 flex items-center gap-3">
        <span className="flex items-center gap-1">
          <FileDiff className="w-3 h-3 text-yellow-400" /> modified
        </span>
        <span className="flex items-center gap-1">
          <FilePlus className="w-3 h-3 text-green-400" /> added
        </span>
      </div>
    </div>
  );
}
