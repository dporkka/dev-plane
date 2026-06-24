'use client';

import { useEffect, useState } from 'react';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { api } from '@/lib/api';
import {
  Github,
  Slack,
  MessageSquare,
  CheckCircle,
  XCircle,
  Plug,
} from 'lucide-react';

interface Integration {
  id: string;
  organization_id: string;
  integration_type: string;
  display_name: string;
  status: string;
}

interface IntegrationDef {
  id: string;
  name: string;
  description: string;
  icon: React.ElementType;
}

const INTEGRATION_DEFS: IntegrationDef[] = [
  {
    id: 'github',
    name: 'GitHub',
    description: 'Connect GitHub repositories, create PRs, and manage webhooks',
    icon: Github,
  },
  {
    id: 'linear',
    name: 'Linear',
    description: 'Sync tasks with Linear issues and track progress',
    icon: CheckCircle,
  },
  {
    id: 'slack',
    name: 'Slack',
    description: 'Get notifications and approve tasks from Slack',
    icon: Slack,
  },
  {
    id: 'discord',
    name: 'Discord',
    description: 'Receive notifications and run commands via Discord',
    icon: MessageSquare,
  },
];

interface IntegrationCardProps {
  definition: IntegrationDef;
  integration?: Integration;
  onConnect: (type: string, token: string, webhookUrl: string) => void;
  onDisconnect: (id: string) => void;
}

function IntegrationCard({
  definition,
  integration,
  onConnect,
  onDisconnect,
}: IntegrationCardProps) {
  const [isEditing, setIsEditing] = useState(false);
  const [token, setToken] = useState('');
  const [webhookUrl, setWebhookUrl] = useState('');
  const isConnected = integration?.status === 'connected';

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onConnect(definition.id, token, webhookUrl);
    setToken('');
    setWebhookUrl('');
    setIsEditing(false);
  };

  return (
    <Card>
      <div className="flex items-start justify-between">
        <div className="flex items-start gap-3">
          <div className={`p-2 rounded-lg ${isConnected ? 'bg-green-500/10' : 'bg-gray-800'}`}>
            <definition.icon className={`w-6 h-6 ${isConnected ? 'text-green-400' : 'text-gray-400'}`} />
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="font-semibold text-white">{definition.name}</h3>
              <StatusBadge status={integration?.status || 'disconnected'} />
            </div>
            <p className="text-sm text-gray-500 mt-1">{definition.description}</p>
          </div>
        </div>
        <button
          onClick={() => {
            if (isConnected && integration) {
              onDisconnect(integration.id);
            } else {
              setIsEditing(!isEditing);
            }
          }}
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

      {!isConnected && isEditing && (
        <form onSubmit={handleSubmit} className="mt-4 space-y-3 border-t border-gray-800 pt-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">
              {definition.id === 'discord' ? 'Bot Token (optional)' : 'API Token'}
            </label>
            <input
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder={definition.id === 'discord' ? 'Bot token' : 'API token'}
              className="w-full bg-gray-900 border border-gray-700 rounded px-3 py-2 text-sm text-white"
            />
          </div>
          {definition.id === 'discord' && (
            <div>
              <label className="block text-sm text-gray-400 mb-1">Webhook URL (optional)</label>
              <input
                type="url"
                value={webhookUrl}
                onChange={(e) => setWebhookUrl(e.target.value)}
                placeholder="https://discord.com/api/webhooks/..."
                className="w-full bg-gray-900 border border-gray-700 rounded px-3 py-2 text-sm text-white"
              />
            </div>
          )}
          <div className="flex gap-2">
            <button type="submit" className="btn-primary text-sm">Save</button>
            <button
              type="button"
              onClick={() => setIsEditing(false)}
              className="btn-secondary text-sm"
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </Card>
  );
}

export default function IntegrationsPage() {
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const orgId = 'current'; // TODO: replace with actual org context when available

  useEffect(() => {
    let cancelled = false;
    api
      .listIntegrations(orgId)
      .then((data) => {
        if (!cancelled) setIntegrations(data);
      })
      .catch((err) => {
        if (!cancelled) setError(err.message || 'Failed to load integrations');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [orgId]);

  const handleConnect = async (type: string, token: string, webhookUrl: string) => {
    try {
      const payload: any = {
        integration_type: type,
        display_name: INTEGRATION_DEFS.find((d) => d.id === type)?.name || type,
      };
      if (token) payload.token = token;
      if (webhookUrl) payload.webhook_url = webhookUrl;
      const created = await api.createIntegration(orgId, payload);
      setIntegrations((prev) => [...prev, created]);
    } catch (err: any) {
      setError(err.message || `Failed to connect ${type}`);
    }
  };

  const handleDisconnect = async (id: string) => {
    try {
      await api.deleteIntegration(id);
      setIntegrations((prev) => prev.filter((i) => i.id !== id));
    } catch (err: any) {
      setError(err.message || 'Failed to disconnect integration');
    }
  };

  const getIntegration = (type: string) =>
    integrations.find((i) => i.integration_type === type);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white">Integrations</h1>
        <p className="text-gray-500 mt-1">
          Connect third-party services to your workspace
        </p>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-400 px-4 py-3 rounded-lg text-sm">
          {error}
        </div>
      )}

      {loading ? (
        <div className="text-gray-500 text-sm">Loading integrations...</div>
      ) : (
        <div className="space-y-4 max-w-3xl">
          {INTEGRATION_DEFS.map((definition) => (
            <IntegrationCard
              key={definition.id}
              definition={definition}
              integration={getIntegration(definition.id)}
              onConnect={handleConnect}
              onDisconnect={handleDisconnect}
            />
          ))}
        </div>
      )}
    </div>
  );
}
