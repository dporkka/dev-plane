import { DevPlaneClient, type RunStream } from "@ai-cp/dev-plane-sdk";
import { decodeTokenClaims } from "./auth-token";

export { decodeTokenClaims };

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

// Minimal EventSource-like interface used for secure SSE streaming.
// Callers can treat the returned object as a drop-in replacement for
// EventSource; it exposes onopen/onmessage/onerror and close().
export interface SSELike {
  onopen: ((this: SSELike, ev: Event) => void) | null;
  onmessage: ((this: SSELike, ev: MessageEvent) => void) | null;
  onerror: ((this: SSELike, ev: Event) => void) | null;
  close(): void;
}

function getToken(): string | undefined {
  if (typeof window === "undefined") return undefined;
  return localStorage.getItem("token") || undefined;
}

function getClient(): DevPlaneClient {
  return new DevPlaneClient({ baseUrl: API_BASE, token: getToken() });
}

// Adapts the SDK's typed RunStream to the SSELike interface expected by
// existing UI components (onmessage receives a MessageEvent with string data).
function adaptRunStream(runStream: RunStream): SSELike {
  const sse: SSELike = {
    onopen: null,
    onmessage: null,
    onerror: null,
    close: () => runStream.close(),
  };

  runStream.onopen = () => {
    sse.onopen?.call(sse, new Event("open"));
  };

  runStream.onmessage = (event) => {
    const data = JSON.stringify(event.data);
    sse.onmessage?.call(sse, new MessageEvent("message", { data }));
  };

  runStream.onerror = () => {
    sse.onerror?.call(sse, new Event("error"));
  };

  return sse;
}

export const api = {
  // ─── Tasks ──────────────────────────────────────────────────────
  listTasks: (projectId: string): Promise<any> =>
    getClient().listTasks(projectId),

  getTask: (id: string): Promise<any> => getClient().getTask(id),

  createTask: (projectId: string, data: any): Promise<any> =>
    getClient().createTask(projectId, data),

  updateTask: (id: string, data: any): Promise<any> =>
    getClient().updateTask(id, data),

  cancelTask: (id: string): Promise<any> => getClient().cancelTask(id),

  approveSpec: (id: string, spec?: any): Promise<any> =>
    getClient().approveSpec(id, spec),

  generateSpec: (id: string): Promise<any> => getClient().generateSpec(id),

  // ─── Spec ───────────────────────────────────────────────────────
  getTaskSpec: (taskId: string): Promise<any> =>
    getClient().getTaskSpec(taskId),

  // ─── Agent Runs ─────────────────────────────────────────────────
  listRuns: (taskId: string): Promise<any> => getClient().listRuns(taskId),

  getRun: (id: string): Promise<any> => getClient().getRun(id),

  getRunSteps: (id: string): Promise<any> => getClient().getRunSteps(id),

  streamRun: (id: string): SSELike => adaptRunStream(getClient().streamRun(id)),

  cancelRun: (id: string): Promise<any> => getClient().cancelRun(id),

  // ─── Reviews ────────────────────────────────────────────────────
  getReview: (runId: string): Promise<any> => getClient().getReview(runId),

  // ─── Pull Requests ──────────────────────────────────────────────
  createPullRequest: (taskId: string): Promise<any> =>
    getClient().createPullRequest(taskId),

  listPullRequests: (projectId: string): Promise<any> =>
    getClient().listPullRequests(projectId),

  // ─── Projects ───────────────────────────────────────────────────
  listProjects: (orgId: string): Promise<any> =>
    getClient().listProjects(orgId),

  getProject: (id: string): Promise<any> => getClient().getProject(id),

  createProject: (orgId: string, data: any): Promise<any> =>
    getClient().createProject(orgId, data),

  // ─── Repositories ───────────────────────────────────────────────
  listRepos: (projectId: string): Promise<any> =>
    getClient().listRepos(projectId),

  getRepo: (id: string): Promise<any> => getClient().getRepo(id),

  connectRepo: (projectId: string, data: any): Promise<any> =>
    getClient().connectRepo(projectId, data),

  disconnectRepo: (id: string): Promise<any> => getClient().disconnectRepo(id),

  syncRepo: (id: string): Promise<any> => getClient().syncRepo(id),

  // ─── Workspaces ─────────────────────────────────────────────────
  listWorkspaces: (taskId: string): Promise<any> =>
    getClient().listWorkspaces(taskId),

  getWorkspace: (id: string): Promise<any> => getClient().getWorkspace(id),

  destroyWorkspace: (id: string): Promise<any> =>
    getClient().destroyWorkspace(id),

  listWorkspaceFiles: (id: string, path?: string): Promise<any> =>
    getClient().listWorkspaceFiles(id, path),

  readWorkspaceFile: (id: string, path: string): Promise<any> =>
    getClient().readWorkspaceFile(id, path),

  writeWorkspaceFile: (
    id: string,
    path: string,
    content: string,
  ): Promise<any> => getClient().writeWorkspaceFile(id, path, content),

  execWorkspaceCommand: (
    id: string,
    command: string,
    timeout?: number,
  ): Promise<any> => getClient().execWorkspaceCommand(id, command, timeout),

  getWorkspaceDiff: (id: string): Promise<any> =>
    getClient().getWorkspaceDiff(id),

  // ─── Approvals ──────────────────────────────────────────────────
  listApprovals: (orgId: string): Promise<any> =>
    getClient().listApprovals(orgId),

  respondApproval: (
    id: string,
    response: "approved" | "rejected",
    note?: string,
  ): Promise<any> => getClient().respondApproval(id, response, note),

  // ─── Dashboard ──────────────────────────────────────────────────
  getDashboard: (orgId: string): Promise<any> =>
    getClient().getDashboard(orgId),

  // ─── Organizations ──────────────────────────────────────────────
  listOrganizations: (): Promise<any> => getClient().listOrganizations(),

  getOrganization: (id: string): Promise<any> =>
    getClient().getOrganization(id),

  createOrganization: (data: any): Promise<any> =>
    getClient().createOrganization(data),

  // ─── Policies ───────────────────────────────────────────────────
  listPolicies: (orgId: string): Promise<any> =>
    getClient().listPolicies(orgId),

  createPolicy: (orgId: string, data: any): Promise<any> =>
    getClient().createPolicy(orgId, data),

  // ─── Integrations ───────────────────────────────────────────────
  listIntegrationProviders: (): Promise<any> =>
    getClient().listIntegrationProviders(),

  listIntegrations: (orgId: string): Promise<any> =>
    getClient().listIntegrations(orgId),

  createIntegration: (orgId: string, data: any): Promise<any> =>
    getClient().createIntegration(orgId, data),

  getIntegration: (id: string): Promise<any> => getClient().getIntegration(id),

  updateIntegration: (id: string, data: any): Promise<any> =>
    getClient().updateIntegration(id, data),

  deleteIntegration: (id: string): Promise<any> =>
    getClient().deleteIntegration(id),

  createVoiceTask: (projectId: string, data: any): Promise<any> =>
    getClient().createVoiceTask(projectId, data),

  // ─── Audit Logs ─────────────────────────────────────────────────
  listAuditLogs: (orgId: string): Promise<any> =>
    getClient().listAuditLogs(orgId),

  // ─── GitHub OAuth ───────────────────────────────────────────────
  githubAuth: () => {
    // Hand off to the Next.js API auth route, which forwards the user to the
    // backend OAuth initiator and then handles the GitHub callback.
    if (typeof window !== "undefined") {
      window.location.href = "/api/auth/github/callback";
    }
  },
};
