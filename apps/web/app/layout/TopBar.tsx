'use client';

import { useStore } from '@/lib/store';
import {
  Bell,
  ChevronDown,
  Building2,
  LogOut,
  User,
  Github,
} from 'lucide-react';
import { useState, useRef, useEffect } from 'react';

export function TopBar() {
  const { user, selectedOrg, organizations, setSelectedOrg, setUser } =
    useStore();
  const [orgDropdownOpen, setOrgDropdownOpen] = useState(false);
  const [userDropdownOpen, setUserDropdownOpen] = useState(false);
  const orgRef = useRef<HTMLDivElement>(null);
  const userRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (orgRef.current && !orgRef.current.contains(e.target as Node)) {
        setOrgDropdownOpen(false);
      }
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setUserDropdownOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const handleLogout = () => {
    localStorage.removeItem('token');
    setUser(null);
    window.location.reload();
  };

  const currentOrg = organizations.find((o) => o.id === selectedOrg);

  return (
    <header className="h-14 border-b border-[#30363d] bg-[#161b22] flex items-center justify-between px-4 flex-shrink-0">
      {/* Organization selector */}
      <div className="relative" ref={orgRef}>
        <button
          onClick={() => setOrgDropdownOpen(!orgDropdownOpen)}
          className="flex items-center gap-2 px-3 py-1.5 rounded-md hover:bg-[#21262d] transition-colors text-sm text-gray-300"
        >
          <Building2 className="w-4 h-4" />
          <span className="font-medium">
            {currentOrg?.name || 'Select Organization'}
          </span>
          <ChevronDown className="w-3 h-3" />
        </button>

        {orgDropdownOpen && (
          <div className="absolute top-full left-0 mt-1 w-56 rounded-md border border-[#30363d] bg-[#161b22] shadow-lg z-50 py-1">
            <div className="px-3 py-2 text-xs text-gray-500 font-medium uppercase tracking-wider">
              Organizations
            </div>
            {organizations.map((org) => (
              <button
                key={org.id}
                onClick={() => {
                  setSelectedOrg(org.id);
                  setOrgDropdownOpen(false);
                }}
                className={`w-full text-left px-3 py-2 text-sm hover:bg-[#21262d] transition-colors flex items-center gap-2 ${
                  selectedOrg === org.id ? 'text-blue-400' : 'text-gray-300'
                }`}
              >
                <Building2 className="w-4 h-4" />
                {org.name}
                {selectedOrg === org.id && (
                  <span className="ml-auto text-xs text-blue-400">&#10003;</span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Right section */}
      <div className="flex items-center gap-2">
        {/* Notifications */}
        <button className="relative p-2 rounded-md hover:bg-[#21262d] text-gray-400 hover:text-gray-200 transition-colors">
          <Bell className="w-5 h-5" />
          <span className="absolute top-1 right-1 w-2 h-2 bg-red-500 rounded-full" />
        </button>

        {/* User dropdown */}
        <div className="relative" ref={userRef}>
          <button
            onClick={() => setUserDropdownOpen(!userDropdownOpen)}
            className="flex items-center gap-2 p-1.5 rounded-md hover:bg-[#21262d] transition-colors"
          >
            {user?.avatar_url ? (
              <img
                src={user.avatar_url}
                alt={user.name || user.email}
                className="w-7 h-7 rounded-full"
              />
            ) : (
              <div className="w-7 h-7 rounded-full bg-blue-600 flex items-center justify-center text-white text-xs font-medium">
                {(user?.name || user?.email || '?')[0].toUpperCase()}
              </div>
            )}
          </button>

          {userDropdownOpen && (
            <div className="absolute top-full right-0 mt-1 w-52 rounded-md border border-[#30363d] bg-[#161b22] shadow-lg z-50 py-1">
              <div className="px-3 py-2 border-b border-[#30363d]">
                <div className="text-sm font-medium text-white">
                  {user?.name || 'User'}
                </div>
                <div className="text-xs text-gray-500">{user?.email}</div>
              </div>
              <button
                onClick={() => {
                  window.location.href = '/settings';
                }}
                className="w-full text-left px-3 py-2 text-sm text-gray-300 hover:bg-[#21262d] transition-colors flex items-center gap-2"
              >
                <User className="w-4 h-4" />
                Profile
              </button>
              <button
                onClick={() => {
                  window.open('https://github.com/settings/connections', '_blank');
                }}
                className="w-full text-left px-3 py-2 text-sm text-gray-300 hover:bg-[#21262d] transition-colors flex items-center gap-2"
              >
                <Github className="w-4 h-4" />
                GitHub Settings
              </button>
              <div className="border-t border-[#30363d] mt-1 pt-1">
                <button
                  onClick={handleLogout}
                  className="w-full text-left px-3 py-2 text-sm text-red-400 hover:bg-[#21262d] transition-colors flex items-center gap-2"
                >
                  <LogOut className="w-4 h-4" />
                  Logout
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
