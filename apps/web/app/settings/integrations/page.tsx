'use client';

import type { ElementType } from 'react';
import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AudioLines,
  CheckCircle,
  Github,
  MessageSquare,
  Plug,
  Slack,
  Webhook,
  XCircle,
} from 'lucide-react';

import { StatusBadge } from '@/components/common/StatusBadge';
import { Loading } from '@/components/common/Loading';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import type { Integration, IntegrationProvider, IntegrationType } from '@/lib/types';

const iconMap: Record<IntegrationType, ElementType> = {
  github: Github,
  linear: CheckCircle,
  slack: Slack,
  discord: MessageSquare,
  webhook: Webhook,
  voice: AudioLines,
};

function defaultConfig(provider: IntegrationProvider, selectedProject: string | null) {
  const baseConfig: Record<string, string> = {};
  provider.required_config_fields.forEach((field) => {
    baseConfig[field] = field === 'project_id' && selectedProject ? selectedProject : '';
  });
  if (provider.type === 'voice') {
    baseConfig.voice_provider = 'whisper';
  }
  return JSON.stringify(baseConfig, null, 2);
}

export default function IntegrationsPage() {
  const queryClient = useQueryClient();
  const { selectedOrg, selectedProject } = useStore();
  const [drafts, setDrafts] = useState<Record<string, { displayName: string; config: string }>>({});
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const { data: providersData, isLoading: providersLoading } = useQuery({
    queryKey: ['integration-providers'],
    queryFn: () => api.listIntegrationProviders(),
  });
  const { data: integrationsData, isLoading: integrationsLoading } = useQuery({
    queryKey: ['integrations', selectedOrg],
    queryFn: () =>
      selectedOrg ? api.listIntegrations(selectedOrg) : Promise.resolve([]),
    enabled: !!selectedOrg,
  });

  const providers: IntegrationProvider[] = providersData?.data || providersData || [];
  const integrations: Integration[] = integrationsData?.data || integrationsData || [];

  const draftValues = useMemo(() => {
    const values: Record<string, { displayName: string; config: string }> = {};
    for (const provider of providers) {
      const existing = integrations.find((item) => item.integration_type === provider.type);
      values[provider.type] = drafts[provider.type] || {
        displayName: existing?.display_name || provider.name,
        config: existing?.config
          ? JSON.stringify(existing.config, null, 2)
          : defaultConfig(provider, selectedProject),
      };
    }
    return values;
  }, [drafts, integrations, providers, selectedProject]);

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['integrations', selectedOrg] });
  };

  const createMutation = useMutation({
    mutationFn: async (provider: IntegrationProvider) => {
      if (!selectedOrg) throw new Error('Select an organization first.');
      const draft = draftValues[provider.type];
      return api.createIntegration(selectedOrg, {
        integration_type: provider.type,
        display_name: draft.displayName,
        config: JSON.parse(draft.config || '{}'),
      });
    },
    onSuccess: refresh,
  });

  const updateMutation = useMutation({
    mutationFn: async ({
      integration,
      provider,
      status,
    }: {
      integration: Integration;
      provider: IntegrationProvider;
      status?: string;
    }) => {
      const draft = draftValues[provider.type];
      return api.updateIntegration(integration.id, {
        display_name: draft.displayName,
        config: JSON.parse(draft.config || '{}'),
        ...(status ? { status } : {}),
      });
    },
    onSuccess: refresh,
  });

  const deleteMutation = useMutation({
    mutationFn: async (integration: Integration) => api.deleteIntegration(integration.id),
    onSuccess: refresh,
  });

  const isBusy = createMutation.isPending || updateMutation.isPending || deleteMutation.isPending;

  if (providersLoading || integrationsLoading) return <Loading />;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white">Integrations</h1>
        <p className="text-gray-500 mt-1">
          Connect Phase 4 providers, review required config, and copy webhook endpoints.
        </p>
      </div>

      {!selectedOrg && (
        <Card>
          <p className="text-sm text-gray-400">
            Select an organization before managing integrations.
          </p>
        </Card>
      )}

      {errorMessage && (
        <Card className="border border-red-500/30">
          <p className="text-sm text-red-300">{errorMessage}</p>
        </Card>
      )}

      <div className="space-y-4 max-w-4xl">
        {providers.map((provider) => {
          const integration = integrations.find((item) => item.integration_type === provider.type);
          const Icon = iconMap[provider.type] || Plug;
          const draft = draftValues[provider.type];
          const status = integration?.status || 'disconnected';
          const isConnected = !!integration && integration.status !== 'disconnected';

          return (
            <Card key={provider.type}>
              <div className="space-y-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex items-start gap-3">
                    <div className={`p-2 rounded-lg ${isConnected ? 'bg-green-500/10' : 'bg-gray-800'}`}>
                      <Icon className={`w-6 h-6 ${isConnected ? 'text-green-400' : 'text-gray-400'}`} />
                    </div>
                    <div className="space-y-2">
                      <div className="flex items-center gap-2">
                        <h3 className="font-semibold text-white">{provider.name}</h3>
                        <StatusBadge status={status} />
                      </div>
                      <p className="text-sm text-gray-500">{provider.description}</p>
                      <div className="flex flex-wrap gap-2 text-xs text-gray-400">
                        {provider.capabilities.map((capability) => (
                          <span
                            key={capability}
                            className="rounded-full border border-[#30363d] px-2 py-0.5"
                          >
                            {capability}
                          </span>
                        ))}
                      </div>
                    </div>
                  </div>

                  <div className="flex gap-2">
                    {integration ? (
                      <>
                        <Button
                          variant="secondary"
                          disabled={!selectedOrg || isBusy}
                          onClick={async () => {
                            setErrorMessage(null);
                            try {
                              await updateMutation.mutateAsync({
                                integration,
                                provider,
                                status: 'connected',
                              });
                            } catch (error) {
                              setErrorMessage(error instanceof Error ? error.message : 'Failed to update integration.');
                            }
                          }}
                        >
                          <Plug className="w-4 h-4" />
                          Save
                        </Button>
                        <Button
                          variant="danger"
                          disabled={!selectedOrg || isBusy}
                          onClick={async () => {
                            setErrorMessage(null);
                            try {
                              await deleteMutation.mutateAsync(integration);
                            } catch (error) {
                              setErrorMessage(error instanceof Error ? error.message : 'Failed to disconnect integration.');
                            }
                          }}
                        >
                          <XCircle className="w-4 h-4" />
                          Disconnect
                        </Button>
                      </>
                    ) : (
                      <Button
                        disabled={!selectedOrg || isBusy}
                        onClick={async () => {
                          setErrorMessage(null);
                          try {
                            await createMutation.mutateAsync(provider);
                          } catch (error) {
                            setErrorMessage(error instanceof Error ? error.message : 'Failed to create integration.');
                          }
                        }}
                      >
                        <Plug className="w-4 h-4" />
                        Connect
                      </Button>
                    )}
                  </div>
                </div>

                <div className="grid gap-4 md:grid-cols-2">
                  <div className="space-y-2">
                    <label className="block text-sm font-medium text-gray-300">
                      Display name
                    </label>
                    <Input
                      value={draft.displayName}
                      onChange={(event) =>
                        setDrafts((current) => ({
                          ...current,
                          [provider.type]: { ...draft, displayName: event.target.value },
                        }))
                      }
                    />
                  </div>

                  <div className="space-y-2">
                    <label className="block text-sm font-medium text-gray-300">
                      Webhook URL
                    </label>
                    <Input readOnly value={integration?.webhook_url || 'Created after connect'} />
                  </div>
                </div>

                <div className="space-y-2">
                  <label className="block text-sm font-medium text-gray-300">
                    Config JSON
                  </label>
                  <Textarea
                    value={draft.config}
                    onChange={(event) =>
                      setDrafts((current) => ({
                        ...current,
                        [provider.type]: { ...draft, config: event.target.value },
                      }))
                    }
                    placeholder='{"project_id":"","repository_id":"","created_by":""}'
                  />
                  <p className="text-xs text-gray-500">
                    Required fields: {provider.required_config_fields.join(', ')}
                  </p>
                  {provider.supports_voice && (
                    <p className="text-xs text-gray-500">
                      Voice tasks use the authenticated <code>/api/v1/projects/:projectID/voice-tasks</code> endpoint with a Whisper transcript payload.
                    </p>
                  )}
                </div>

                {integration?.last_synced_at && (
                  <p className="text-xs text-gray-500">
                    Last synced at {new Date(integration.last_synced_at).toLocaleString()}
                  </p>
                )}
              </div>
            </Card>
          );
        })}
      </div>
    </div>
  );
}
