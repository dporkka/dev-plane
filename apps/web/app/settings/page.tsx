'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Card } from '@/components/ui/card';
import { Tabs } from '@/components/ui/tabs';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
  Settings,
  Building2,
  Plug,
  Shield,
  Cpu,
  Save,
} from 'lucide-react';

export default function SettingsPage() {
  const [orgName, setOrgName] = useState('');
  const [orgSlug, setOrgSlug] = useState('');

  const tabs = [
    { id: 'general', label: 'General', icon: Building2 },
    { id: 'integrations', label: 'Integrations', icon: Plug },
    { id: 'policies', label: 'Policies', icon: Shield },
    { id: 'models', label: 'Models', icon: Cpu },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white">Organization Settings</h1>
        <p className="text-gray-500 mt-1">Manage your organization preferences</p>
      </div>

      {/* Settings tabs as links */}
      <div className="flex gap-2 border-b border-[#30363d]">
        {tabs.map((tab) => (
          <Link
            key={tab.id}
            href={
              tab.id === 'general'
                ? '/settings'
                : `/settings/${tab.id}`
            }
            className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
              tab.id === 'general'
                ? 'border-blue-500 text-blue-400'
                : 'border-transparent text-gray-400 hover:text-gray-200'
            }`}
          >
            <tab.icon className="w-4 h-4" />
            {tab.label}
          </Link>
        ))}
      </div>

      {/* General Settings */}
      <div className="max-w-2xl space-y-6">
        <Card>
          <div className="space-y-4">
            <div className="flex items-center gap-2 mb-4">
              <Building2 className="w-5 h-5 text-blue-400" />
              <h2 className="text-lg font-semibold text-white">
                Organization Details
              </h2>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1">
                Organization Name
              </label>
              <Input
                value={orgName}
                onChange={(e) => setOrgName(e.target.value)}
                placeholder="My Organization"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1">
                Slug
              </label>
              <Input
                value={orgSlug}
                onChange={(e) => setOrgSlug(e.target.value)}
                placeholder="my-org"
              />
              <p className="text-xs text-gray-500 mt-1">
                Used in URLs and API references
              </p>
            </div>
            <div className="flex justify-end pt-2">
              <Button className="btn-primary flex items-center gap-2">
                <Save className="w-4 h-4" />
                Save Changes
              </Button>
            </div>
          </div>
        </Card>

        <Card>
          <div className="space-y-4">
            <div className="flex items-center gap-2 mb-4">
              <Settings className="w-5 h-5 text-yellow-400" />
              <h2 className="text-lg font-semibold text-white">
                Danger Zone
              </h2>
            </div>
            <div className="flex items-center justify-between">
              <div>
                <div className="text-white font-medium">Delete Organization</div>
                <div className="text-sm text-gray-500">
                  This action cannot be undone
                </div>
              </div>
              <button className="px-4 py-2 bg-red-600 hover:bg-red-500 text-white rounded-md font-medium transition-colors text-sm">
                Delete
              </button>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
}
