'use client';

import { useCallback } from 'react';
import { useParams } from 'next/navigation';
import dynamic from 'next/dynamic';
import { GitFork, FolderTree } from 'lucide-react';

const RepoArchitectureGraph = dynamic(
  () =>
    import('@/components/graph/RepoArchitectureGraph').then(
      (mod) => mod.RepoArchitectureGraph
    ),
  {
    ssr: false,
    loading: () => (
      <div className="flex items-center justify-center h-full">
        <div className="text-gray-500">Loading repository graph...</div>
      </div>
    ),
  }
);

export default function ProjectArchitecturePage() {
  const params = useParams();
  const projectId = params.id as string;

  const handleNodeClick = useCallback(
    (path: string) => {
      // In a real implementation, this would navigate to a file viewer
      // or open the file in the code editor
      console.log(`Navigate to file: ${path} (project: ${projectId})`);
    },
    [projectId]
  );

  return (
    <div className="space-y-4 h-full">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white flex items-center gap-2">
            <FolderTree size={24} className="text-emerald-400" />
            Repository Architecture
          </h1>
          <p className="text-gray-500 mt-1">
            Interactive dependency graph for project{' '}
            <span className="text-blue-400 font-mono text-sm">{projectId}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-500 bg-gray-800 px-2 py-1 rounded border border-gray-700">
            Demo View
          </span>
        </div>
      </div>

      <div
        className="rounded-lg border border-gray-700 bg-[#0d1117] overflow-hidden"
        style={{ height: 'calc(100vh - 200px)' }}
      >
        <RepoArchitectureGraph onNodeClick={handleNodeClick} />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2 flex items-center gap-2">
            <FolderTree size={14} className="text-blue-400" />
            Directory Structure
          </h3>
          <p className="text-xs text-gray-400">
            Directories are shown as blue nodes. Click to explore subdirectories. The graph shows the full hierarchy from root to leaf files.
          </p>
        </div>
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2 flex items-center gap-2">
            <GitFork size={14} className="text-violet-400" />
            Import Dependencies
          </h3>
          <p className="text-xs text-gray-400">
            Dashed violet edges show import relationships between packages. Orange dashed edges indicate inter-package dependencies.
          </p>
        </div>
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2">File Types</h3>
          <p className="text-xs text-gray-400">
            Files are color-coded by extension: TypeScript (blue), Go (green), CSS (pink), JSON (yellow), SQL (orange), Markdown (gray).
          </p>
        </div>
      </div>
    </div>
  );
}
