export interface DevPlaneClientOptions {
  baseUrl: string;
  token?: string;
}

export interface IntegrationPayload {
  integration_type: string;
  display_name: string;
  config?: Record<string, unknown>;
}

export interface VoiceTaskPayload {
  repository_id: string;
  transcript: string;
  title?: string;
  provider?: string;
  metadata?: Record<string, unknown>;
}

export class DevPlaneClient {
  constructor(private readonly options: DevPlaneClientOptions) {}

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await fetch(`${this.options.baseUrl}${path}`, {
      ...init,
      headers: {
        'Content-Type': 'application/json',
        ...(this.options.token ? { Authorization: 'Bearer ' + this.options.token } : {}),
        ...init?.headers,
      },
    });

    if (!response.ok) {
      throw new Error((await response.text()) || `HTTP ${response.status}`);
    }

    if (response.status === 204) {
      return undefined as T;
    }
    return response.json() as Promise<T>;
  }

  listIntegrationProviders() {
    return this.request('/api/v1/integrations/providers');
  }

  listIntegrations(orgId: string) {
    return this.request(`/api/v1/organizations/${orgId}/integrations`);
  }

  createIntegration(orgId: string, payload: IntegrationPayload) {
    return this.request(`/api/v1/organizations/${orgId}/integrations`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  updateIntegration(integrationId: string, payload: Partial<IntegrationPayload> & { status?: string }) {
    return this.request(`/api/v1/integrations/${integrationId}`, {
      method: 'PATCH',
      body: JSON.stringify(payload),
    });
  }

  createVoiceTask(projectId: string, payload: VoiceTaskPayload) {
    return this.request(`/api/v1/projects/${projectId}/voice-tasks`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  getOpenAPISpec() {
    return this.request('/api/public/v1/openapi.json');
  }
}
