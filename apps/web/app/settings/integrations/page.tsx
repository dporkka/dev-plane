'use client';

import { useState } from 'react';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/common/StatusBadge';
import {
  Github,
  Slack,
  MessageSquare,
  CheckCircle,
  XCircle,
  RefreshCw,
  Plug,
} from 'lucide-react';

interface IntegrationCardProps {
  name: string;
  description: string;
  icon: React.ElementType;
  status: string;
  onConnect: () => void;
  onDisconnect: () => void;
}

function IntegrationCard({
  name,
  description,
  icon: Icon,
  status,
  onConnect,
  onDisconnect,
}: IntegrationCardProps) {
  const isConnected = status === 'connected';

  return (
    <Card>
      <div className="flex items-start justify-between">
        <div className="flex items-start gap-3">
          <div className={`p-2 rounded-lg ${isConnected ? 'bg-green-500/10' : 'bg-gray-800'}`}>
            <Icon className={`w-6 h-6 ${isConnected ? 'text-green-400' : 'text-gray-400'}`} />
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="font-semibold text-white">{name}</h3>
              <StatusBadge status={status} />
            </div>
            <p className="text-sm text-gray-500 mt-1">{description}</p>
          </div>
        </div>
        <button
          onClick={isConnected ? onDisconnect : onConnect}
          className={isConnected ? 'btn-secondary text-red-400' : 'btn-primary'}
        >
          {isConnected ? (
            <>
              <XCircle className="w-4 h-4 mr-1" />
              Disconnect
            </>
          ) : (
            <>
              <Plug className="w-4 h-4 mr-1" />
              Connect
            </>
          )}
        </button>
      </div>
    </Card>
  );
}

export default function IntegrationsPage() {
  const integrations = [
    {
      id: 'github',
      name: 'GitHub',
      description: 'Connect GitHub repositories, create PRs, and manage webhooks',
      icon: Github,
      status: 'connected',
    },
    {
      id: 'linear',
      name: 'Linear',
      description: 'Sync tasks with Linear issues and track progress',
      icon: CheckCircle,
      status: 'disconnected',
    },
    {
      id: 'slack',
      name: 'Slack',
      description: 'Get notifications and approve tasks from Slack',
      icon: Slack,
      status: 'disconnected',
    },
    {
      id: 'discord',
      name: 'Discord',
      description: 'Receive notifications and run commands via Discord',
      icon: MessageSquare,
      status: 'pending',
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white">Integrations</h1>
        <p className="text-gray-500 mt-1">
          Connect third-party services to your workspace
        </p>
      </div>

      <div className="space-y-4 max-w-3xl">
        {integrations.map((integration) => (
          <IntegrationCard
            key={integration.id}
            name={integration.name}
            description={integration.description}
            icon={integration.icon}
            status={integration.status}
            onConnect={() => console.log('Connect', integration.id)}
            onDisconnect={() => console.log('Disconnect', integration.id)}
          />
        ))}
      </div>
    </div>
  );
}
