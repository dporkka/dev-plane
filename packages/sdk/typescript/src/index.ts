import type {
  AgentRun,
  AgentStep,
  Approval,
  ApprovalResponse,
  AuditLog,
  ConnectRepositoryRequest,
  CreateBriefHandoffRequest,
  CreateOrganizationRequest,
  CreatePolicyRequest,
  CreateProjectRequest,
  CreatePullRequestRequest,
  CreateSecretRequest,
  CreateTaskRequest,
  DashboardData,
  DeployTaskRequest,
  DeploymentResponse,
  DestroyWorkspaceResponse,
  ExecRequest,
  ExecResult,
  GenerateSpecResponse,
  HealthStatus,
  Integration,
  IntegrationPayload,
  IntegrationProvider,
  MergePullRequestRequest,
  Organization,
  PatchRequest,
  PatchResponse,
  Policy,
  Project,
  PullRequest,
  RepoAnalysis,
  RepoAnalysisPending,
  Repository,
  RespondApprovalRequest,
  RetryRunResponse,
  ReviewReport,
  RotateSecretRequest,
  RunEvent,
  RunStreamEvent,
  SecretReference,
  StartRunResponse,
  StartServiceRequest,
  StartServiceResponse,
  StatusResponse,
  StopServiceRequest,
  StopServiceResponse,
  Task,
  TaskSpec,
  UpdateIntegrationPayload,
  UpdateTaskRequest,
  VoiceTaskPayload,
  Workspace,
  WorkspaceDiffResponse,
  WorkspaceFileContent,
  WorkspaceFileEntry,
  WriteWorkspaceFileRequest,
  WriteWorkspaceFileResponse,
} from './types.js';

export * from './types.js';

export interface DevPlaneClientOptions {
  baseUrl: string;
  token?: string;
}

/**
 * RunStream consumes the server-sent events stream for an agent run.
 * It exposes typed callbacks instead of leaking JWTs into query strings.
 */
export class RunStream {
  readonly url: string;
  readyState: number;

  onopen: (() => void) | null = null;
  onmessage: ((event: { data: RunStreamEvent }) => void) | null = null;
  onerror: ((error: Error) => void) | null = null;

  private controller: AbortController;

  constructor(url: string, token?: string) {
    this.url = url;
    this.readyState = 0; // CONNECTING
    this.controller = new AbortController();
    this.connect(token);
  }

  private connect(token?: string) {
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
          this.onopen();
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
          this.onerror(err instanceof Error ? err : new Error(String(err)));
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
        this.onerror(err instanceof Error ? err : new Error(String(err)));
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
    try {
      const payload = JSON.parse(data) as RunStreamEvent;
      if (this.onmessage) {
        this.onmessage({ data: payload });
      }
    } catch {
      if (this.onerror) {
        this.onerror(new Error('invalid SSE payload'));
      }
    }
  }

  close() {
    this.readyState = 2; // CLOSED
    this.controller.abort();
  }
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

  private fetchRaw(path: string, init?: RequestInit): Promise<Response> {
    return fetch(`${this.options.baseUrl}${path}`, {
      ...init,
      headers: {
        ...(this.options.token ? { Authorization: 'Bearer ' + this.options.token } : {}),
        ...init?.headers,
      },
    });
  }

  // ─── Health ───────────────────────────────────────────────────────

  health() {
    return this.request<HealthStatus>('/health');
  }

  ready() {
    return this.request<HealthStatus>('/ready');
  }

  // ─── OpenAPI Spec ─────────────────────────────────────────────────

  getOpenAPISpec() {
    return this.request<Record<string, unknown>>('/api/public/v1/openapi.json');
  }

  // ─── Organizations ────────────────────────────────────────────────

  listOrganizations() {
    return this.request<Organization[]>('/api/v1/organizations');
  }

  getOrganization(id: string) {
    return this.request<Organization>(`/api/v1/organizations/${id}`);
  }

  createOrganization(payload: CreateOrganizationRequest) {
    return this.request<Organization>('/api/v1/organizations', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Projects ─────────────────────────────────────────────────────

  listProjects(orgId: string) {
    return this.request<Project[]>(`/api/v1/organizations/${orgId}/projects`);
  }

  getProject(id: string) {
    return this.request<Project>(`/api/v1/projects/${id}`);
  }

  createProject(orgId: string, payload: CreateProjectRequest) {
    return this.request<Project>(`/api/v1/organizations/${orgId}/projects`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Repositories ─────────────────────────────────────────────────

  listRepositories(projectId: string) {
    return this.request<Repository[]>(`/api/v1/projects/${projectId}/repositories`);
  }

  getRepository(id: string) {
    return this.request<Repository>(`/api/v1/repositories/${id}`);
  }

  connectRepository(projectId: string, payload: ConnectRepositoryRequest) {
    return this.request<Repository>(`/api/v1/projects/${projectId}/repositories`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  disconnectRepository(id: string) {
    return this.request<void>(`/api/v1/repositories/${id}`, { method: 'DELETE' });
  }

  syncRepository(id: string) {
    return this.request<StatusResponse>(`/api/v1/repositories/${id}/sync`, { method: 'POST' });
  }

  analyzeRepository(id: string) {
    return this.request<RepoAnalysis | RepoAnalysisPending>(`/api/v1/repositories/${id}/analyze`, {
      method: 'POST',
    });
  }

  // Aliases matching the original SDK naming
  listRepos = this.listRepositories;
  getRepo = this.getRepository;
  connectRepo = this.connectRepository;
  disconnectRepo = this.disconnectRepository;
  syncRepo = this.syncRepository;

  // ─── Tasks ────────────────────────────────────────────────────────

  listTasks(projectId: string, options?: { status?: Task['status'] }) {
    const params = new URLSearchParams();
    if (options?.status) {
      params.set('status', options.status);
    }
    const qs = params.toString();
    return this.request<Task[]>(
      `/api/v1/projects/${projectId}/tasks${qs ? `?${qs}` : ''}`,
    );
  }

  getTask(id: string) {
    return this.request<Task>(`/api/v1/tasks/${id}`);
  }

  getTaskSpec(taskId: string) {
    return this.request<TaskSpec>(`/api/v1/tasks/${taskId}/spec`);
  }

  createTask(projectId: string, payload: CreateTaskRequest) {
    return this.request<Task>(`/api/v1/projects/${projectId}/tasks`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  createBriefHandoff(projectId: string, payload: CreateBriefHandoffRequest) {
    return this.request<Task>(`/api/v1/projects/${projectId}/brief-handoffs`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  updateTask(id: string, payload: UpdateTaskRequest) {
    return this.request<Task>(`/api/v1/tasks/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(payload),
    });
  }

  cancelTask(id: string) {
    return this.request<StatusResponse>(`/api/v1/tasks/${id}/cancel`, { method: 'POST' });
  }

  approveSpec(id: string, spec?: Record<string, unknown>) {
    return this.request<StatusResponse>(`/api/v1/tasks/${id}/approve-spec`, {
      method: 'POST',
      body: spec ? JSON.stringify(spec) : undefined,
    });
  }

  generateSpec(id: string) {
    return this.request<GenerateSpecResponse>(`/api/v1/tasks/${id}/generate-spec`, {
      method: 'POST',
    });
  }

  startRun(id: string) {
    return this.request<StartRunResponse>(`/api/v1/tasks/${id}/start-run`, { method: 'POST' });
  }

  deployTask(id: string, payload: DeployTaskRequest) {
    return this.request<DeploymentResponse>(`/api/v1/tasks/${id}/deploy`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Agent Runs ───────────────────────────────────────────────────

  listRuns(taskId: string) {
    return this.request<AgentRun[]>(`/api/v1/tasks/${taskId}/runs`);
  }

  getRun(id: string) {
    return this.request<AgentRun>(`/api/v1/runs/${id}`);
  }

  getRunSteps(id: string) {
    return this.request<AgentStep[]>(`/api/v1/runs/${id}/steps`);
  }

  getRunEvents(id: string) {
    return this.request<RunEvent[]>(`/api/v1/runs/${id}/events`);
  }

  cancelRun(id: string) {
    return this.request<StatusResponse>(`/api/v1/runs/${id}/cancel`, { method: 'POST' });
  }

  retryRun(id: string) {
    return this.request<RetryRunResponse>(`/api/v1/runs/${id}/retry`, { method: 'POST' });
  }

  streamRun(id: string) {
    return new RunStream(`${this.options.baseUrl}/api/v1/runs/${id}/stream`, this.options.token);
  }

  // ─── Reviews ──────────────────────────────────────────────────────

  getReview(runId: string) {
    return this.request<ReviewReport>(`/api/v1/runs/${runId}/review`);
  }

  requestReview(runId: string) {
    return this.request<ReviewReport>(`/api/v1/runs/${runId}/review`, { method: 'POST' });
  }

  // ─── Approvals ────────────────────────────────────────────────────

  listTaskApprovals(taskId: string) {
    return this.request<Approval[]>(`/api/v1/tasks/${taskId}/approvals`);
  }

  listOrganizationApprovals(orgId: string) {
    return this.request<Approval[]>(`/api/v1/organizations/${orgId}/approvals`);
  }

  listApprovals(orgId: string) {
    return this.listOrganizationApprovals(orgId);
  }

  respondApproval(id: string, response: ApprovalResponse, note?: string) {
    return this.request<Approval>(`/api/v1/approvals/${id}/respond`, {
      method: 'POST',
      body: JSON.stringify({ response, response_note: note } satisfies RespondApprovalRequest),
    });
  }

  // ─── Pull Requests ────────────────────────────────────────────────

  listPullRequests(projectId: string) {
    return this.request<PullRequest[]>(`/api/v1/projects/${projectId}/pull-requests`);
  }

  getPullRequest(id: string) {
    return this.request<PullRequest>(`/api/v1/pull-requests/${id}`);
  }

  createPullRequest(taskId: string, payload?: CreatePullRequestRequest) {
    return this.request<PullRequest>(`/api/v1/tasks/${taskId}/pull-request`, {
      method: 'POST',
      body: payload ? JSON.stringify(payload) : undefined,
    });
  }

  mergePullRequest(id: string, payload?: MergePullRequestRequest) {
    return this.request<PullRequest>(`/api/v1/pull-requests/${id}/merge`, {
      method: 'POST',
      body: payload ? JSON.stringify(payload) : undefined,
    });
  }

  // ─── Workspaces ───────────────────────────────────────────────────

  listWorkspaces(taskId: string) {
    return this.request<Workspace[]>(`/api/v1/tasks/${taskId}/workspaces`);
  }

  getWorkspace(id: string) {
    return this.request<Workspace>(`/api/v1/workspaces/${id}`);
  }

  destroyWorkspace(id: string) {
    return this.request<DestroyWorkspaceResponse>(`/api/v1/workspaces/${id}/destroy`, {
      method: 'POST',
    });
  }

  getWorkspaceDiff(id: string) {
    return this.request<WorkspaceDiffResponse>(`/api/v1/workspaces/${id}/diff`);
  }

  listWorkspaceFiles(id: string, path?: string) {
    const qs = path ? `?path=${encodeURIComponent(path)}` : '';
    return this.request<WorkspaceFileEntry[]>(`/api/v1/workspaces/${id}/files${qs}`);
  }

  readWorkspaceFile(id: string, path: string) {
    const qs = `?path=${encodeURIComponent(path)}`;
    return this.request<WorkspaceFileContent>(`/api/v1/workspaces/${id}/files/content${qs}`);
  }

  writeWorkspaceFile(id: string, path: string, content: string) {
    return this.request<WriteWorkspaceFileResponse>(`/api/v1/workspaces/${id}/files/write`, {
      method: 'POST',
      body: JSON.stringify({ path, content } satisfies WriteWorkspaceFileRequest),
    });
  }

  applyWorkspacePatch(id: string, payload: PatchRequest) {
    return this.request<PatchResponse>(`/api/v1/workspaces/${id}/patch`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  execWorkspaceCommand(id: string, command: string, timeout?: number) {
    return this.request<ExecResult>(`/api/v1/workspaces/${id}/exec`, {
      method: 'POST',
      body: JSON.stringify({ command, timeout } satisfies ExecRequest),
    });
  }

  startWorkspaceService(id: string, payload: StartServiceRequest) {
    return this.request<StartServiceResponse>(`/api/v1/workspaces/${id}/start-service`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  stopWorkspaceService(id: string, payload: StopServiceRequest) {
    return this.request<StopServiceResponse>(`/api/v1/workspaces/${id}/stop-service`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Artifacts ────────────────────────────────────────────────────

  async getArtifact(id: string): Promise<Blob> {
    const response = await this.fetchRaw(`/api/v1/artifacts/${id}`);
    if (!response.ok) {
      throw new Error((await response.text()) || `HTTP ${response.status}`);
    }
    return response.blob();
  }

  // ─── Policies ─────────────────────────────────────────────────────

  listPolicies(orgId: string) {
    return this.request<Policy[]>(`/api/v1/organizations/${orgId}/policies`);
  }

  createPolicy(orgId: string, payload: CreatePolicyRequest) {
    return this.request<Policy>(`/api/v1/organizations/${orgId}/policies`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Audit Logs ───────────────────────────────────────────────────

  listAuditLogs(orgId: string, options?: { limit?: number }) {
    const params = new URLSearchParams();
    if (options?.limit) {
      params.set('limit', String(options.limit));
    }
    const qs = params.toString();
    return this.request<AuditLog[]>(
      `/api/v1/organizations/${orgId}/audit-logs${qs ? `?${qs}` : ''}`,
    );
  }

  // ─── Secrets ──────────────────────────────────────────────────────

  listSecrets(orgId: string) {
    return this.request<SecretReference[]>(`/api/v1/organizations/${orgId}/secrets`);
  }

  createSecret(orgId: string, payload: CreateSecretRequest) {
    return this.request<SecretReference>(`/api/v1/organizations/${orgId}/secrets`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  rotateSecret(id: string, payload: RotateSecretRequest) {
    return this.request<SecretReference>(`/api/v1/secrets/${id}/rotate`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  // ─── Dashboard ────────────────────────────────────────────────────

  getDashboard(orgId: string) {
    return this.request<DashboardData>(`/api/v1/organizations/${orgId}/dashboard`);
  }

  // ─── Integrations ─────────────────────────────────────────────────

  listIntegrationProviders() {
    return this.request<IntegrationProvider[]>('/api/v1/integrations/providers');
  }

  listIntegrations(orgId: string) {
    return this.request<Integration[]>(`/api/v1/organizations/${orgId}/integrations`);
  }

  createIntegration(orgId: string, payload: IntegrationPayload) {
    return this.request<Integration>(`/api/v1/organizations/${orgId}/integrations`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  getIntegration(id: string) {
    return this.request<Integration>(`/api/v1/integrations/${id}`);
  }

  updateIntegration(id: string, payload: UpdateIntegrationPayload) {
    return this.request<Integration>(`/api/v1/integrations/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(payload),
    });
  }

  deleteIntegration(id: string) {
    return this.request<void>(`/api/v1/integrations/${id}`, { method: 'DELETE' });
  }

  // ─── Voice Tasks ──────────────────────────────────────────────────

  createVoiceTask(projectId: string, payload: VoiceTaskPayload) {
    return this.request<Task>(`/api/v1/projects/${projectId}/voice-tasks`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }
}
