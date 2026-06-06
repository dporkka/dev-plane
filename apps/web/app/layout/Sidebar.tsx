'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useStore } from '@/lib/store';
import {
  LayoutDashboard,
  FolderGit,
  GitBranch,
  Settings,
  Activity,
  ChevronLeft,
  ChevronRight,
  Terminal,
} from 'lucide-react';

const navItems = [
  { href: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { href: '/projects', label: 'Projects', icon: FolderGit },
  { href: '/repositories', label: 'Repositories', icon: GitBranch },
  { href: '/settings', label: 'Settings', icon: Settings },
];

export function Sidebar() {
  const pathname = usePathname();
  const { sidebarOpen, toggleSidebar } = useStore();

  return (
    <aside
      className={`${
        sidebarOpen ? 'w-60' : 'w-16'
      } flex-shrink-0 border-r border-[#30363d] bg-[#0d1117] transition-all duration-200 flex flex-col`}
    >
      {/* Logo */}
      <div className="flex items-center gap-3 h-14 px-4 border-b border-[#30363d]">
        <Activity className="w-6 h-6 text-blue-500 flex-shrink-0" />
        {sidebarOpen && (
          <span className="font-bold text-lg tracking-tight text-white truncate">
            AI Control
          </span>
        )}
        <button
          onClick={toggleSidebar}
          className="ml-auto p-1 rounded hover:bg-[#21262d] text-gray-400 transition-colors flex-shrink-0"
          title={sidebarOpen ? 'Collapse sidebar' : 'Expand sidebar'}
        >
          {sidebarOpen ? (
            <ChevronLeft className="w-4 h-4" />
          ) : (
            <ChevronRight className="w-4 h-4" />
          )}
        </button>
      </div>

      {/* Navigation */}
      <nav className="p-3 space-y-1 flex-1">
        {navItems.map((item) => {
          const isActive = pathname === item.href || pathname.startsWith(`${item.href}/`);
          return (
            <Link
              key={item.href}
              href={item.href}
              className={`sidebar-link ${isActive ? 'active' : ''}`}
              title={!sidebarOpen ? item.label : undefined}
            >
              <item.icon className="w-5 h-5 flex-shrink-0" />
              {sidebarOpen && item.label}
            </Link>
          );
        })}
      </nav>

      {/* Footer */}
      <div className="p-3 border-t border-[#30363d]">
        <div
          className="sidebar-link cursor-default"
          title={!sidebarOpen ? 'v0.1.0' : undefined}
        >
          <Terminal className="w-5 h-5 flex-shrink-0" />
          {sidebarOpen && (
            <span className="text-xs text-gray-500">v0.1.0</span>
          )}
        </div>
      </div>
    </aside>
  );
}
