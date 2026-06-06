// Types generated from OpenAPI spec.
// These types match the Go backend models exactly and should be used
// as the source of truth for all API request/response types.
//
// To extend, import from here rather than redefining.

// ─── Enums ───────────────────────────────────────────────────────────

export type TaskStatus =
  | 'backlog'
  | 'spec_review'
  | 'approved'
  | 'running'
  | 'reviewing'
  | 'pr_created'
  | 'done'
  | 'failed'
  | 'cancelled';

export type Priority = 'low' | 'medium' | 'high' | 'urgent';

export type RiskLevel = 'low' | 'medium' | 'high' | 'critical';

export type AgentRole =
  | 'planner'
  | 'implementer'
  | 'reviewer'
  | 'test_runner'
  | 'security_reviewer'
  | 'docs_writer'
  | 'release_manager';

export type RunStatus = 'pending' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';

export type StepType =
  | 'thought'
  | 'tool_call'
  | 'command_run'
  | 'file_patch'
  | 'approval_request'
  | 'message'
  | 'error';

export type StepStatus = 'pending' | 'running' | 'completed' | 'failed';

export type ConnectionStatus = 'pending' | 'connected' | 'error';

export type UserRole = 'owner' | 'admin' | 'member';

export type Plan = 'free' | 'pro' | 'enterprise';

export type ApprovalType = 'spec' | 'execution' | 'deploy' | 'risky_action';

export type ApprovalResponse = 'approved' | 'rejected';

export type ResourceType = 'file' | 'command' | 'secret' | 'deploy' | 'git' | 'network';

export type Action = 'read' | 'write' | 'execute' | 'delete';

export type Effect = 'allow' | 'ask' | 'deny' | 'admin_only';

export type IntegrationType = 'github' | 'linear' | 'slack' | 'discord';

export type IntegrationStatus = 'pending' | 'connected' | 'error' | 'disconnected';

export type WorkspaceStatus =
  | 'pending'
  | 'preparing'
  | 'ready'
  | 'running'
  | 'stopped'
  | 'error'
  | 'destroyed';

export type PRState = 'open' | 'closed' | 'merged';

export type MessageType =
  | 'handoff'
  | 'review_comment'
  | 'blocker'
  | 'escalation'
  | 'watchdog'
  | 'decision'
  | 'question'
  | 'answer';

export type SecretProvider = 'sops' | 'env' | 'vault' | 'encrypted_db';

export type SecretScope = 'dev' | 'staging' | 'prod';

// ─── Core Models ─────────────────────────────────────────────────────

export interface Organization {
  id: string;
  name: string;
  slug: string;
  plan: Plan;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Project {
  id: string;
  organization_id: string;
  name: string;
  slug: string;
  description?: string;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Repository {
  id: string;
  project_id: string;
  github_id?: number;
  owner: string;
  name: string;
  full_name: string;
  clone_url: string;
  default_branch: string;
  private: boolean;
  connection_status: ConnectionStatus;
  last_synced_at?: string;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Task {
  id: string;
  project_id: string;
  repository_id: string;
  workspace_id?: string;
  created_by: string;
  source: string;
  source_id?: string;
  title: string;
  description?: string;
  status: TaskStatus;
  priority: Priority;
  risk_level: RiskLevel;
  target_branch: string;
  spec?: Record<string, unknown>;
  acceptance_criteria?: unknown[];
  max_cost?: number;
  max_runtime_minutes: number;
  approval_requirements?: unknown[];
  metadata?: Record<string, unknown>;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface AgentRun {
  id: string;
  task_id: string;
  workspace_id?: string;
  agent_role: AgentRole;
  model?: string;
  provider?: string;
  status: RunStatus;
  started_at?: string;
  completed_at?: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_cost: number;
  error_message?: string;
  summary?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface AgentStep {
  id: string;
  agent_run_id: string;
  step_number: number;
  step_type: StepType;
  status: StepStatus;
  content?: string;
  tool_name?: string;
  tool_input?: Record<string, unknown>;
  tool_output?: Record<string, unknown>;
  command?: string;
  command_output?: string;
  exit_code?: number;
  file_path?: string;
  diff?: string;
  cost: number;
  latency_ms: number;
  created_at: string;
}

export interface Approval {
  id: string;
  task_id: string;
  agent_run_id?: string;
  approval_type: ApprovalType;
  requested_by: string;
  requested_at: string;
  responded_by?: string;
  response?: ApprovalResponse;
  response_note?: string;
  responded_at?: string;
  expires_at?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface Policy {
  id: string;
  organization_id: string;
  project_id?: string;
  name: string;
  resource_type: ResourceType;
  action: Action;
  effect: Effect;
  conditions?: Record<string, unknown>;
  priority: number;
  created_at: string;
  updated_at: string;
}

export interface Integration {
  id: string;
  organization_id: string;
  integration_type: IntegrationType;
  display_name: string;
  config?: Record<string, unknown>;
  status: IntegrationStatus;
  webhook_url?: string;
  last_synced_at?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Workspace {
  id: string;
  repository_id: string;
  task_id?: string;
  name: string;
  branch: string;
  base_branch: string;
  worktree_path?: string;
  runtime_provider: string;
  runtime_session_id?: string;
  status: WorkspaceStatus;
  preview_url?: string;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface User {
  id: string;
  organization_id: string;
  email: string;
  name?: string;
  avatar_url?: string;
  role: UserRole;
  github_id?: string;
  github_username?: string;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

// ─── New Domain Models ───────────────────────────────────────────────

export interface AgentMessage {
  id: string;
  task_id: string;
  run_id?: string;
  from_agent: string;
  to_agent: string;
  message_type: MessageType;
  content: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface SecretReference {
  id: string;
  organization_id: string;
  project_id?: string;
  name: string;
  scope: SecretScope;
  provider: SecretProvider;
  key_path: string;
  description?: string;
  last_rotated_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PullRequest {
  id: string;
  task_id: string;
  run_id?: string;
  repository_id: string;
  number: number;
  title: string;
  body: string;
  branch: string;
  base_branch: string;
  url: string;
  state: PRState;
  draft: boolean;
  created_by: string;
  merged_at?: string;
  created_at: string;
  updated_at: string;
}

export interface Budget {
  id: string;
  organization_id: string;
  project_id?: string;
  name: string;
  max_cost_per_run: number;
  max_runtime_minutes: number;
  max_model_calls: number;
  max_tool_calls: number;
  max_shell_commands: number;
  max_files_changed: number;
  max_daily_spend: number;
  max_concurrent_agents: number;
  max_prs_per_day: number;
  alerts_enabled: boolean;
  created_at: string;
  updated_at: string;
}

// ─── Dashboard Types ─────────────────────────────────────────────────

export interface DashboardStats {
  active_runs: number;
  tasks_today: number;
  cost_today: number;
  pending_approvals: number;
}

export interface DashboardData {
  stats: DashboardStats;
  active_runs: AgentRun[];
  recent_tasks: Task[];
}

// ─── Request/Response Types ──────────────────────────────────────────

export interface CreateTaskRequest {
  repository_id: string;
  title: string;
  description?: string;
  priority?: Priority;
  risk_level?: RiskLevel;
  target_branch?: string;
  max_cost?: number;
  spec?: Record<string, unknown>;
}

export interface UpdateTaskRequest {
  title?: string;
  description?: string;
  priority?: Priority;
  risk_level?: RiskLevel;
  status?: TaskStatus;
}

export interface CreateProjectRequest {
  name: string;
  slug: string;
  description?: string;
}

export interface CreateOrganizationRequest {
  name: string;
  slug: string;
  plan?: Plan;
}

export interface RespondApprovalRequest {
  response: ApprovalResponse;
  response_note?: string;
}

// ─── API Response Wrappers ───────────────────────────────────────────

export interface ApiListResponse<T> {
  data: T[];
  total: number;
  page: number;
  per_page: number;
}

export interface ApiError {
  error: string;
  message?: string;
  code?: string;
}

export interface HealthStatus {
  status: string;
}
