'use client';

import dynamic from 'next/dynamic';
import { Network, Info } from 'lucide-react';

const SystemArchitectureGraph = dynamic(
  () =>
    import('@/components/graph/SystemArchitectureGraph').then(
      (mod) => mod.SystemArchitectureGraph
    ),
  {
    ssr: false,
    loading: () => (
      <div className="flex items-center justify-center h-full">
        <div className="text-gray-500">Loading architecture graph...</div>
      </div>
    ),
  }
);

export default function ArchitecturePage() {
  return (
    <div className="space-y-4 h-full">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white flex items-center gap-2">
            <Network size={24} className="text-blue-400" />
            System Architecture
          </h1>
          <p className="text-gray-500 mt-1">
            Interactive view of the AI Dev Control Plane service topology
          </p>
        </div>
      </div>

      <div className="rounded-lg border border-gray-700 bg-[#0d1117] overflow-hidden" style={{ height: 'calc(100vh - 200px)' }}>
        <SystemArchitectureGraph />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2">Client Layer</h3>
          <p className="text-xs text-gray-400">
            The Web UI (Next.js 16) provides the primary interface. Future clients include CLI tools and API integrations.
          </p>
        </div>
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2">API Layer</h3>
          <p className="text-xs text-gray-400">
            Go API server with Chi router handles authentication, authorization, REST endpoints, and WebSocket connections.
          </p>
        </div>
        <div className="card">
          <h3 className="text-sm font-semibold text-gray-200 mb-2">Runtime Layer</h3>
          <p className="text-xs text-gray-400">
            Workers consume tasks from NATS, provision local or Docker workspace runtimes, and persist runtime session metadata. HTTP workspace operations and agent tools dispatch through the runtime provider for Docker sessions.
          </p>
        </div>
      </div>
    </div>
  );
}
