'use client';

import { useState } from 'react';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import {
  Shield,
  Plus,
  Edit3,
  Trash2,
  FileText,
} from 'lucide-react';

interface PolicyItem {
  id: string;
  name: string;
  resource_type: string;
  action: string;
  effect: string;
  priority: number;
}

export default function PoliciesPage() {
  const [policies, setPolicies] = useState<PolicyItem[]>([
    {
      id: '1',
      name: 'Protect main branch',
      resource_type: 'git',
      action: 'write',
      effect: 'admin_only',
      priority: 100,
    },
    {
      id: '2',
      name: 'No production secrets',
      resource_type: 'secret',
      action: 'read',
      effect: 'deny',
      priority: 90,
    },
    {
      id: '3',
      name: 'Review destructive commands',
      resource_type: 'command',
      action: 'execute',
      effect: 'ask',
      priority: 80,
    },
    {
      id: '4',
      name: 'Allow file reads',
      resource_type: 'file',
      action: 'read',
      effect: 'allow',
      priority: 10,
    },
  ]);

  const getEffectColor = (effect: string) => {
    switch (effect) {
      case 'allow': return 'bg-green-500/10 text-green-400 border-green-500/30';
      case 'deny': return 'bg-red-500/10 text-red-400 border-red-500/30';
      case 'ask': return 'bg-yellow-500/10 text-yellow-400 border-yellow-500/30';
      case 'admin_only': return 'bg-purple-500/10 text-purple-400 border-purple-500/30';
      default: return 'bg-gray-500/10 text-gray-400';
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Policy Rules</h1>
          <p className="text-gray-500 mt-1">
            Define what agents can and cannot do
          </p>
        </div>
        <button className="btn-primary flex items-center gap-2">
          <Plus className="w-4 h-4" />
          New Policy
        </button>
      </div>

      <div className="space-y-2">
        {policies.map((policy) => (
          <Card key={policy.id}>
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <Shield className="w-5 h-5 text-gray-400" />
                <div>
                  <div className="flex items-center gap-2">
                    <span className="text-white font-medium">{policy.name}</span>
                    <Badge
                      variant="outline"
                      className={getEffectColor(policy.effect)}
                    >
                      {policy.effect}
                    </Badge>
                  </div>
                  <div className="text-xs text-gray-500 mt-1 flex items-center gap-3">
                    <span>Resource: {policy.resource_type}</span>
                    <span>Action: {policy.action}</span>
                    <span>Priority: {policy.priority}</span>
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-1">
                <button className="p-2 rounded-md hover:bg-gray-800 text-gray-400 hover:text-gray-200 transition-colors">
                  <Edit3 className="w-4 h-4" />
                </button>
                <button
                  onClick={() =>
                    setPolicies(policies.filter((p) => p.id !== policy.id))
                  }
                  className="p-2 rounded-md hover:bg-red-500/10 text-gray-400 hover:text-red-400 transition-colors"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          </Card>
        ))}
      </div>

      {policies.length === 0 && (
        <div className="text-center py-12 text-gray-500">
          <Shield className="w-12 h-12 mx-auto mb-3 text-gray-700" />
          <p className="text-lg font-medium">No policies defined</p>
          <p className="text-sm mt-1">
            Create policies to control what agents can do
          </p>
        </div>
      )}
    </div>
  );
}
