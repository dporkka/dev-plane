'use client';

import React from 'react';
import { cn } from '@/lib/utils';
import { X } from 'lucide-react';

export interface EditorTab {
  id: string;
  label: string;
  isDirty?: boolean;
}

export interface RightTab {
  id: string;
  label: string;
  icon: React.ElementType;
}

interface WorkspaceTabsProps {
  editorTabs: EditorTab[];
  activeEditorTab: string | null;
  onSelectEditorTab: (id: string) => void;
  onCloseEditorTab: (id: string) => void;
  rightTabs: RightTab[];
  activeRightTab: string;
  onSelectRightTab: (id: string) => void;
}

export function WorkspaceTabs({
  editorTabs,
  activeEditorTab,
  onSelectEditorTab,
  onCloseEditorTab,
  rightTabs,
  activeRightTab,
  onSelectRightTab,
}: WorkspaceTabsProps) {
  if (editorTabs.length === 0 && rightTabs.length === 0) return null;

  return (
    <div className="flex items-center justify-between border-b border-[#30363d] bg-[#0d1117]">
      {/* Editor tabs */}
      <div className="flex items-center overflow-x-auto flex-1 min-w-0">
        {editorTabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onSelectEditorTab(tab.id)}
            className={cn(
              'group flex items-center gap-1.5 px-3 py-2 text-xs border-r border-[#30363d] transition-colors min-w-0',
              activeEditorTab === tab.id
                ? 'bg-[#0d1117] text-white border-b-0'
                : 'text-gray-500 hover:text-gray-300 hover:bg-[#161b22]'
            )}
          >
            <span className="truncate max-w-[120px]">{tab.label}</span>
            {tab.isDirty && <span className="text-yellow-400">●</span>}
            <span
              onClick={(e) => {
                e.stopPropagation();
                onCloseEditorTab(tab.id);
              }}
              className="ml-1 p-0.5 rounded hover:bg-[#30363d] opacity-0 group-hover:opacity-100 transition-opacity"
            >
              <X className="w-3 h-3" />
            </span>
          </button>
        ))}
      </div>

      {/* Right panel tabs */}
      {rightTabs.length > 0 && (
        <div className="flex items-center border-l border-[#30363d] flex-shrink-0">
          {rightTabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => onSelectRightTab(tab.id)}
              className={cn(
                'flex items-center gap-1.5 px-3 py-2 text-xs border-l border-[#30363d] transition-colors',
                activeRightTab === tab.id
                  ? 'bg-[#0d1117] text-blue-400'
                  : 'text-gray-500 hover:text-gray-300 hover:bg-[#161b22]'
              )}
              title={tab.label}
            >
              <tab.icon className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">{tab.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
