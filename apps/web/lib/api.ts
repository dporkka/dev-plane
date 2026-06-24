const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';

// Minimal EventSource-like interface used for secure SSE streaming.
// Callers can treat the returned object as a drop-in replacement for
// EventSource; it exposes onopen/onmessage/onerror and close().
export interface SSELike {
  onopen: ((this: SSELike, ev: Event) => void) | null;
  onmessage: ((this: SSELike, ev: MessageEvent) => void) | null;
  onerror: ((this: SSELike, ev: Event) => void) | null;
  close(): void;
}

// SecureEventSource fetches an SSE stream using the Authorization header
// instead of leaking the JWT into the URL query string.
class SecureEventSource implements SSELike {
  url: string;
  readyState: number;

  onopen: ((this: SSELike, ev: Event) => void) | null = null;
  onmessage: ((this: SSELike, ev: MessageEvent) => void) | null = null;
  onerror: ((this: SSELike, ev: Event) => void) | null = null;

  private controller: AbortController;

  constructor(url: string) {
    this.url = url;
    this.readyState = 0; // CONNECTING
    this.controller = new AbortController();
    this.connect();
  }

  private connect() {
    const token = typeof window !== 'undefined' ? localStorage.getItem('token') : null;

    fetch(this.url, {
      method: 'GET',
      headers: {
        Accept: 'text/event-stream',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      signal: this.controller.signal,
    })
      .then((response) => {
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        if (!response.body) {
          throw new Error('response body is null');
        }
        this.readyState = 1; // OPEN
        if (this.onopen) {
          this.onopen(new Event('open'));
        }
        this.readStream(response.body);
      })
      .catch((err) => {
        if ((err as Error).name === 'AbortError') {
          this.readyState = 2; // CLOSED
          return;
        }
        this.readyState = 2; // CLOSED
        if (this.onerror) {
          this.onerror(new Event('error'));
        }
      });
  }

  private async readStream(body: ReadableStream<Uint8Array>) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    try {
      while (this.readyState !== 2) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';
        for (const line of lines) {
          this.processLine(line);
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError' && this.onerror) {
        this.onerror(new Event('error'));
      }
    } finally {
      this.readyState = 2; // CLOSED
      reader.releaseLock();
    }
  }

  private processLine(line: string) {
    const trimmed = line.trim();
    if (!trimmed.startsWith('data:')) {
      return;
    }
    const data = trimmed.slice('data:'.length).trim();
    const event = new MessageEvent('message', { data });
    if (this.onmessage) {
      this.onmessage(event);
    }
  }

  close() {
    this.readyState = 2; // CLOSED
    this.controller.abort();
  }
}

// SSE stream helper
function streamSSE(path: string): SSELike {
  const url = new URL(`${API_BASE}${path}`, window.location.href);
  return new SecureEventSource(url.toString());
}

async function fetchAPI(path: string, options?: RequestInit) {
  const token = typeof window !== 'undefined' ? localStorage.getItem('token') : null;
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(token && { Authorization: `Bearer ${token}` }),
      ...options?.headers,
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
  const contentType = res.headers.get('content-type');
  if (contentType?.includes('application/json')) {
    return res.json();
  }
  return res.text();
}


export const api = {
  // ─── Tasks ──────────────────────────────────────────────────────
  listTasks: (projectId: string) =>
    fetchAPI(`/api/v1/projects/${projectId}/tasks`),

  getTask: (id: string) =>
    fetchAPI(`/api/v1/tasks/${id}`),

  createTask: (projectId: string, data: any) =>
    fetchAPI(`/api/v1/projects/${projectId}/tasks`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  updateTask: (id: string, data: any) =>
    fetchAPI(`/api/v1/tasks/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(data),
    }),

  cancelTask: (id: string) =>
    fetchAPI(`/api/v1/tasks/${id}/cancel`, { method: 'POST' }),

  approveSpec: (id: string, spec?: any) =>
    fetchAPI(`/api/v1/tasks/${id}/approve-spec`, {
      method: 'POST',
      body: spec ? JSON.stringify(spec) : undefined,
    }),

  generateSpec: (id: string) =>
    fetchAPI(`/api/v1/tasks/${id}/generate-spec`, { method: 'POST' }),

  // ─── Spec ───────────────────────────────────────────────────────
  getTaskSpec: (taskId: string) =>
    fetchAPI(`/api/v1/tasks/${taskId}/spec`),

  // ─── Agent Runs ─────────────────────────────────────────────────
  listRuns: (taskId: string) =>
    fetchAPI(`/api/v1/tasks/${taskId}/runs`),

  getRun: (id: string) =>
    fetchAPI(`/api/v1/runs/${id}`),

  getRunSteps: (id: string) =>
    fetchAPI(`/api/v1/runs/${id}/steps`),

  streamRun: (id: string) =>
    streamSSE(`/api/v1/runs/${id}/stream`),

  cancelRun: (id: string) =>
    fetchAPI(`/api/v1/runs/${id}/cancel`, { method: 'POST' }),

  // ─── Reviews ────────────────────────────────────────────────────
  getReview: (runId: string) =>
    fetchAPI(`/api/v1/runs/${runId}/review`),

  // ─── Pull Requests ──────────────────────────────────────────────
  createPullRequest: (taskId: string) =>
    fetchAPI(`/api/v1/tasks/${taskId}/pull-request`, { method: 'POST' }),

  listPullRequests: (projectId: string) =>
    fetchAPI(`/api/v1/projects/${projectId}/pull-requests`),

  // ─── Projects ───────────────────────────────────────────────────
  listProjects: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/projects`),

  getProject: (id: string) =>
    fetchAPI(`/api/v1/projects/${id}`),

  createProject: (orgId: string, data: any) =>
    fetchAPI(`/api/v1/organizations/${orgId}/projects`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // ─── Repositories ───────────────────────────────────────────────
  listRepos: (projectId: string) =>
    fetchAPI(`/api/v1/projects/${projectId}/repositories`),

  getRepo: (id: string) =>
    fetchAPI(`/api/v1/repositories/${id}`),

  connectRepo: (projectId: string, data: any) =>
    fetchAPI(`/api/v1/projects/${projectId}/repositories`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  disconnectRepo: (id: string) =>
    fetchAPI(`/api/v1/repositories/${id}`, { method: 'DELETE' }),

  syncRepo: (id: string) =>
    fetchAPI(`/api/v1/repositories/${id}/sync`, { method: 'POST' }),

  // ─── Workspaces ─────────────────────────────────────────────────
  listWorkspaces: (taskId: string) =>
    fetchAPI(`/api/v1/tasks/${taskId}/workspaces`),

  getWorkspace: (id: string) =>
    fetchAPI(`/api/v1/workspaces/${id}`),

  destroyWorkspace: (id: string) =>
    fetchAPI(`/api/v1/workspaces/${id}/destroy`, { method: 'POST' }),

  listWorkspaceFiles: (id: string, path?: string) =>
    fetchAPI(`/api/v1/workspaces/${id}/files${path ? `?path=${encodeURIComponent(path)}` : ''}`),

  readWorkspaceFile: (id: string, path: string) =>
    fetchAPI(`/api/v1/workspaces/${id}/files/content?path=${encodeURIComponent(path)}`),

  writeWorkspaceFile: (id: string, path: string, content: string) =>
    fetchAPI(`/api/v1/workspaces/${id}/files/write`, {
      method: 'POST',
      body: JSON.stringify({ path, content }),
    }),

  execWorkspaceCommand: (id: string, command: string, timeout?: number) =>
    fetchAPI(`/api/v1/workspaces/${id}/exec`, {
      method: 'POST',
      body: JSON.stringify({ command, timeout }),
    }),

  getWorkspaceDiff: (id: string) =>
    fetchAPI(`/api/v1/workspaces/${id}/diff`),

  // ─── Approvals ──────────────────────────────────────────────────
  listApprovals: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/approvals`),

  respondApproval: (id: string, response: 'approved' | 'rejected', note?: string) =>
    fetchAPI(`/api/v1/approvals/${id}/respond`, {
      method: 'POST',
      body: JSON.stringify({ response, response_note: note }),
    }),

  // ─── Dashboard ──────────────────────────────────────────────────
  getDashboard: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/dashboard`),

  // ─── Organizations ──────────────────────────────────────────────
  listOrganizations: () =>
    fetchAPI(`/api/v1/organizations`),

  getOrganization: (id: string) =>
    fetchAPI(`/api/v1/organizations/${id}`),

  createOrganization: (data: any) =>
    fetchAPI(`/api/v1/organizations`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // ─── Policies ───────────────────────────────────────────────────
  listPolicies: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/policies`),

  createPolicy: (orgId: string, data: any) =>
    fetchAPI(`/api/v1/organizations/${orgId}/policies`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // ─── Integrations ───────────────────────────────────────────────
  listIntegrations: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/integrations`),

  createIntegration: (orgId: string, data: any) =>
    fetchAPI(`/api/v1/organizations/${orgId}/integrations`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  updateIntegration: (id: string, data: any) =>
    fetchAPI(`/api/v1/integrations/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(data),
    }),

  deleteIntegration: (id: string) =>
    fetchAPI(`/api/v1/integrations/${id}`, { method: 'DELETE' }),

  // ─── Audit Logs ─────────────────────────────────────────────────
  listAuditLogs: (orgId: string) =>
    fetchAPI(`/api/v1/organizations/${orgId}/audit-logs`),

  // ─── GitHub OAuth ───────────────────────────────────────────────
  githubAuth: () => {
    const clientId = process.env.NEXT_PUBLIC_GITHUB_CLIENT_ID;
    const redirectUri = `${typeof window !== 'undefined' ? window.location.origin : ''}/api/auth/github/callback`;
    const url = `https://github.com/login/oauth/authorize?client_id=${clientId}&redirect_uri=${redirectUri}&scope=repo,read:org`;
    if (typeof window !== 'undefined') {
      window.location.href = url;
    }
  },
};
