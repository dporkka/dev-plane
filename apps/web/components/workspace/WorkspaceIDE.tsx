'use client';

import React, { useState, useCallback, useEffect } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { FileBrowser } from './FileBrowser';
import { WorkspaceTabs } from './WorkspaceTabs';
import { RunStatusBar } from './RunStatusBar';
import type { RunStatus } from './RunStatusBar';
import type { EditorTab, RightTab } from './WorkspaceTabs';
import type { FileWithStatus } from './FileBrowser';
import { CodeEditor } from '@/components/code/CodeMirror';
import { DiffViewer } from '@/components/code/DiffViewer';
import { Terminal } from '@/components/run/Terminal';
import type { FileNode } from '@/components/code/FileTree';
import {
  Terminal as TerminalIcon,
  Globe,
  ScrollText,
  FileCode,
  FolderGit,
  Loader2,
} from 'lucide-react';

interface WorkspaceIDEProps {
  workspaceId: string;
  initialRunStatus?: RunStatus;
}

interface OpenFile {
  path: string;
  content: string;
  language: 'typescript' | 'javascript' | 'json' | 'markdown';
  isDirty: boolean;
}

export function WorkspaceIDE({ workspaceId, initialRunStatus }: WorkspaceIDEProps) {
  const [editorTabs, setEditorTabs] = useState<EditorTab[]>([]);
  const [activeEditorTab, setActiveEditorTab] = useState<string | null>(null);
  const [activeRightTab, setActiveRightTab] = useState('terminal');
  const [openFiles, setOpenFiles] = useState<Map<string, OpenFile>>(new Map());
  const [terminalLogs, setTerminalLogs] = useState<string[]>([]);
  const [diffContent, setDiffContent] = useState<string>('');
  const [showDiff, setShowDiff] = useState(false);

  const [runStatus, setRunStatus] = useState<RunStatus>(initialRunStatus || {
    status: 'idle',
    currentStep: 0,
    totalSteps: 0,
    currentAction: '',
    cost: 0,
    elapsedSeconds: 0,
  });

  // Fetch file tree
  const { data: fileTreeData, isLoading: filesLoading } = useQuery({
    queryKey: ['workspace-files', workspaceId],
    queryFn: () => api.listWorkspaceFiles(workspaceId),
    enabled: !!workspaceId,
  });

  // Fetch diff
  const { data: diffData } = useQuery({
    queryKey: ['workspace-diff', workspaceId],
    queryFn: () => api.getWorkspaceDiff(workspaceId),
    enabled: !!workspaceId,
    retry: false,
  });

  // Fetch workspace for run data
  const { data: workspace } = useQuery({
    queryKey: ['workspace', workspaceId],
    queryFn: () => api.getWorkspace(workspaceId),
    enabled: !!workspaceId,
  });

  // Write file mutation
  const writeMutation = useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      api.writeWorkspaceFile(workspaceId, path, content),
  });

  // Exec command mutation
  const execMutation = useMutation({
    mutationFn: (command: string) => api.execWorkspaceCommand(workspaceId, command),
    onSuccess: (data: any) => {
      if (data?.output) {
        setTerminalLogs((prev) => [...prev, data.output]);
      }
    },
  });

  const fileTree: FileWithStatus[] = React.useMemo(() => {
    const raw = fileTreeData?.data || fileTreeData || [];
    return normalizeFileTree(raw);
  }, [fileTreeData]);

  useEffect(() => {
    if (diffData && typeof diffData === 'string') {
      setDiffContent(diffData);
    } else if (diffData?.diff) {
      setDiffContent(diffData.diff);
    }
  }, [diffData]);

  const handleSelectFile = useCallback(
    async (node: FileWithStatus) => {
      if (node.type === 'directory') return;

      const path = node.path;
      const existing = openFiles.get(path);
      if (existing) {
        setActiveEditorTab(path);
        return;
      }

      try {
        const result = await api.readWorkspaceFile(workspaceId, path);
        const content = typeof result === 'string' ? result : result.content || '';
        const lang = detectLanguage(path);

        const openFile: OpenFile = {
          path,
          content,
          language: lang,
          isDirty: false,
        };

        setOpenFiles((prev) => new Map(prev).set(path, openFile));
        setEditorTabs((prev) => [
          ...prev,
          { id: path, label: node.name, isDirty: false },
        ]);
        setActiveEditorTab(path);
        setShowDiff(false);
      } catch (err) {
        setTerminalLogs((prev) => [
          ...prev,
          `[Error] Failed to load ${path}: ${(err as Error).message}`,
        ]);
      }
    },
    [workspaceId, openFiles]
  );

  const handleCloseEditorTab = useCallback(
    (id: string) => {
      setEditorTabs((prev) => {
        const filtered = prev.filter((t) => t.id !== id);
        if (activeEditorTab === id) {
          setActiveEditorTab(filtered.length > 0 ? filtered[filtered.length - 1].id : null);
        }
        return filtered;
      });
      setOpenFiles((prev) => {
        const next = new Map(prev);
        next.delete(id);
        return next;
      });
    },
    [activeEditorTab]
  );

  const handleFileChange = useCallback(
    (path: string, value: string) => {
      setOpenFiles((prev) => {
        const file = prev.get(path);
        if (!file) return prev;
        const next = new Map(prev);
        next.set(path, { ...file, content: value, isDirty: true });
        return next;
      });
      setEditorTabs((prev) =>
        prev.map((t) => (t.id === path ? { ...t, isDirty: true } : t))
      );
    },
    []
  );

  const handleSaveFile = useCallback(
    async (path: string) => {
      const file = openFiles.get(path);
      if (!file || !file.isDirty) return;
      try {
        await writeMutation.mutateAsync({ path, content: file.content });
        setOpenFiles((prev) => {
          const next = new Map(prev);
          next.set(path, { ...file, isDirty: false });
          return next;
        });
        setEditorTabs((prev) =>
          prev.map((t) => (t.id === path ? { ...t, isDirty: false } : t))
        );
        setTerminalLogs((prev) => [...prev, `[Saved] ${path}`]);
      } catch (err) {
        setTerminalLogs((prev) => [
          ...prev,
          `[Error] Failed to save ${path}: ${(err as Error).message}`,
        ]);
      }
    },
    [openFiles, writeMutation]
  );

  const handleRequestDiff = useCallback((path: string) => {
    setShowDiff(true);
    setActiveEditorTab(null);
  }, []);

  const activeFile = activeEditorTab ? openFiles.get(activeEditorTab) : null;

  const rightTabs: RightTab[] = [
    { id: 'terminal', label: 'Terminal', icon: TerminalIcon },
    { id: 'preview', label: 'Preview', icon: Globe },
    { id: 'logs', label: 'Logs', icon: ScrollText },
  ];

  // Keyboard shortcut: Ctrl+S to save
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        if (activeEditorTab) {
          handleSaveFile(activeEditorTab);
        }
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [activeEditorTab, handleSaveFile]);

  return (
    <div className="flex flex-col h-full">
      {/* Top bar */}
      <div className="h-10 border-b border-[#30363d] bg-[#161b22] flex items-center px-3 flex-shrink-0 gap-3">
        <FolderGit className="w-4 h-4 text-blue-400" />
        <span className="text-sm font-medium text-white truncate">
          Workspace #{workspaceId.slice(0, 8)}
        </span>
        {workspace?.branch && (
          <span className="text-xs text-gray-500 bg-[#21262d] px-2 py-0.5 rounded">
            {workspace.branch}
          </span>
        )}
        {writeMutation.isPending && (
          <span className="text-xs text-blue-400 flex items-center gap-1">
            <Loader2 className="w-3 h-3 animate-spin" />
            Saving...
          </span>
        )}
      </div>

      {/* Tabs */}
      <WorkspaceTabs
        editorTabs={editorTabs}
        activeEditorTab={activeEditorTab}
        onSelectEditorTab={(id) => {
          setActiveEditorTab(id);
          setShowDiff(false);
        }}
        onCloseEditorTab={handleCloseEditorTab}
        rightTabs={rightTabs}
        activeRightTab={activeRightTab}
        onSelectRightTab={setActiveRightTab}
      />

      {/* Main content */}
      <div className="flex-1 flex overflow-hidden">
        {/* Left: File tree */}
        <div className="w-56 flex-shrink-0 border-r border-[#30363d] overflow-hidden">
          <FileBrowser
            files={fileTree}
            selectedPath={activeEditorTab || undefined}
            onSelectFile={handleSelectFile}
            onRequestDiff={handleRequestDiff}
            isLoading={filesLoading}
          />
        </div>

        {/* Center: Editor / Diff */}
        <div className="flex-1 flex flex-col min-w-0">
          {showDiff && diffContent ? (
            <div className="flex-1 overflow-auto">
              <DiffViewer oldValue="" newValue={diffContent} filename="Changes" />
            </div>
          ) : activeFile ? (
            <div className="flex-1 overflow-auto">
              <CodeEditor
                value={activeFile.content}
                language={activeFile.language}
                onChange={(value) => handleFileChange(activeFile.path, value)}
                height="100%"
              />
            </div>
          ) : (
            <div className="flex-1 flex items-center justify-center text-gray-600">
              <div className="text-center">
                <FileCode className="w-12 h-12 mx-auto mb-3 text-gray-700" />
                <p className="text-sm">Select a file to start editing</p>
                <p className="text-xs text-gray-700 mt-1">Ctrl+S to save</p>
              </div>
            </div>
          )}
        </div>

        {/* Right: Terminal / Preview / Logs */}
        <div className="w-96 flex-shrink-0 border-l border-[#30363d] bg-[#0d1117] flex flex-col">
          {activeRightTab === 'terminal' && (
            <div className="flex-1 flex flex-col">
              <div className="flex-1 overflow-auto p-2">
                <Terminal logs={terminalLogs} height="100%" />
              </div>
              <div className="h-8 border-t border-[#30363d] flex items-center px-2">
                <span className="text-gray-600 text-xs">$</span>
                <input
                  type="text"
                  className="flex-1 bg-transparent text-xs text-gray-300 ml-2 outline-none"
                  placeholder="Type command..."
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      const cmd = e.currentTarget.value;
                      if (cmd.trim()) {
                        setTerminalLogs((prev) => [...prev, `$ ${cmd}`]);
                        execMutation.mutate(cmd);
                        e.currentTarget.value = '';
                      }
                    }
                  }}
                />
              </div>
            </div>
          )}
          {activeRightTab === 'preview' && (
            <div className="flex-1 flex items-center justify-center">
              {workspace?.preview_url ? (
                <iframe
                  src={workspace.preview_url}
                  className="w-full h-full border-0"
                  title="Preview"
                />
              ) : (
                <div className="text-center text-gray-600 p-4">
                  <Globe className="w-10 h-10 mx-auto mb-2 text-gray-700" />
                  <p className="text-sm">Preview unavailable</p>
                  <p className="text-xs text-gray-700 mt-1">
                    Start the dev server to see a live preview
                  </p>
                </div>
              )}
            </div>
          )}
          {activeRightTab === 'logs' && (
            <div className="flex-1 overflow-auto p-2">
              <Terminal logs={terminalLogs} height="100%" />
            </div>
          )}
        </div>
      </div>

      {/* Bottom status bar */}
      <RunStatusBar runStatus={runStatus} />
    </div>
  );
}

// Helpers
function normalizeFileTree(raw: any[]): FileWithStatus[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((item) => ({
    name: item.name || '',
    type: item.type || 'file',
    path: item.path || '',
    language: item.language,
    gitStatus: item.git_status,
    children: item.children ? normalizeFileTree(item.children) : undefined,
  }));
}

function detectLanguage(path: string): 'typescript' | 'javascript' | 'json' | 'markdown' {
  if (path.endsWith('.ts') || path.endsWith('.tsx')) return 'typescript';
  if (path.endsWith('.js') || path.endsWith('.jsx')) return 'javascript';
  if (path.endsWith('.json')) return 'json';
  if (path.endsWith('.md')) return 'markdown';
  return 'typescript';
}
