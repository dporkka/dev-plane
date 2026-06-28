// TypeScript types for the AI Dev Control Plane public API.
// These mirror the Go backend models and the OpenAPI spec.

// ─── Enums ───────────────────────────────────────────────────────────

export type TaskStatus =
  | 'backlog'
  | 'spec_review'
  | 'approved'
  | 'running'
  | 'reviewing'
  | 'pr_created'
  | 'deploying'
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

export type RunStatus =
  | 'pending'
  | 'queued'
  | 'running'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'cancelled';

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

export type ApprovalType = 'spec' | 'execution' | 'pr_create' | 'deploy' | 'risky_action';

export type ApprovalResponse = 'approved' | 'rejected';

export type ResourceType = 'file' | 'command' | 'secret' | 'deploy' | 'git' | 'network';

export type Action = 'read' | 'write' | 'execute' | 'delete';

export type Effect = 'allow' | 'ask' | 'deny' | 'admin_only';

export type IntegrationType =
  | 'github'
  | 'linear'
  | 'slack'
  | 'discord'
  | 'webhook'
  | 'voice';

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

export interface TaskSpec {
  id: string;
  task_id: string;
  summary: string;
  problem_statement: string;
  implementation_plan: string[];
  files_to_change: string[];
  files_to_create: string[];
  acceptance_criteria: string[];
  test_plan: string;
  risk_assessment: string;
  rollback_plan: string;
  required_approvals: string[];
  estimated_cost: number;
  recommended_agent: string;
  generated_by: string;
  generated_at: string;
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

export interface IntegrationProvider {
  type: IntegrationType;
  name: string;
  description: string;
  capabilities: string[];
  required_config_fields: string[];
  supports_webhook: boolean;
  supports_commands: boolean;
  supports_voice: boolean;
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
  provider?: IntegrationProvider;
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

// ─── Artifact & Audit Types ──────────────────────────────────────────

export interface Artifact {
  id: string;
  agent_run_id?: string;
  step_id?: string;
  artifact_type: string;
  file_name: string;
  file_path: string;
  mime_type?: string;
  size_bytes?: number;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AuditLog {
  id: string;
  organization_id: string;
  actor_type: string;
  actor_id?: string;
  action: string;
  resource_type: string;
  resource_id?: string;
  details?: Record<string, unknown>;
  ip_address?: string;
  user_agent?: string;
  created_at: string;
}

// ─── Repository Analysis Types ───────────────────────────────────────

export interface RepoLanguage {
  name: string;
  file_count: number;
}

export interface RepoDependency {
  name: string;
  version: string;
  source: string;
  type: string;
}

export interface DirEntry {
  name: string;
  is_dir: boolean;
}

export interface RepoAnalysis {
  repository_id: string;
  languages: RepoLanguage[];
  package_managers: string[];
  frameworks: string[];
  test_commands: string[];
  build_commands: string[];
  dependencies?: RepoDependency[];
  entry_points: string[];
  has_dockerfile: boolean;
  has_ci_config: boolean;
  structure: DirEntry[];
  analyzed_at: string;
}

export interface RepoAnalysisPending {
  repository_id: string;
  status: 'pending_clone';
  message: string;
  clone_url: string;
}

// ─── Run Event Types ─────────────────────────────────────────────────

export interface RunEvent {
  id: string;
  agent_run_id: string;
  event_type: string;
  status: string;
  message?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface RunStreamEvent {
  run_id: string;
  status: RunStatus;
}

// ─── Review Types ────────────────────────────────────────────────────

export interface Finding {
  severity: 'critical' | 'high' | 'medium' | 'low' | 'info';
  file: string;
  line: number;
  message: string;
  category: 'correctness' | 'security' | 'performance' | 'style' | 'testing';
  suggestion?: string;
}

export interface FileChange {
  path: string;
  status: 'added' | 'modified' | 'deleted';
  insertions: number;
  deletions: number;
  is_test: boolean;
  is_config: boolean;
  is_migration: boolean;
}

export interface DiffSummary {
  files_changed: number;
  insertions: number;
  deletions: number;
  files: FileChange[];
}

export interface ReviewReport {
  run_id: string;
  summary: string;
  findings: Finding[];
  risk_level: RiskLevel;
  approvable: boolean;
  suggestions: string[];
  test_coverage: string;
  security_notes: string;
  diff_summary: DiffSummary;
  created_at: string;
}

// ─── Workspace Operation Types ───────────────────────────────────────

export interface WorkspaceFileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size?: number;
}

export interface WorkspaceFileContent {
  path: string;
  content: string;
  size: number;
}

export interface WriteWorkspaceFileRequest {
  path: string;
  content: string;
}

export interface WriteWorkspaceFileResponse {
  status: 'written';
  path: string;
}

export interface PatchRequest {
  patch: string;
}

export interface PatchResponse {
  status: 'patched';
  output: string;
}

export interface ExecRequest {
  command: string;
  timeout?: number;
}

export interface ExecResult {
  command: string;
  stdout: string;
  exit_code: number;
}

export interface StartServiceRequest {
  command: string;
  port: number;
  name?: string;
}

export interface StartServiceResponse {
  service_id: string;
  status: 'running';
  pid: number;
  port: number;
  log_path: string;
}

export interface StopServiceRequest {
  service_id: string;
}

export interface StopServiceResponse {
  service_id: string;
  status: 'stopped';
}

export interface WorkspaceDiffResponse {
  diff: string;
}

// ─── Request/Response Types ──────────────────────────────────────────

export interface CreateOrganizationRequest {
  name: string;
  slug: string;
  plan?: Plan;
}

export interface CreateProjectRequest {
  name: string;
  slug: string;
  description?: string;
}

export interface ConnectRepositoryRequest {
  owner: string;
  name: string;
}

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

export interface CreateBriefHandoffRequest {
  repository_id: string;
  brief_project_id?: string;
  brief_url?: string;
  brief_zip_url?: string;
  title?: string;
  description?: string;
  priority?: Priority;
  risk_level?: RiskLevel;
  target_branch?: string;
  acceptance_criteria?: string[];
  documents?: BriefDocument[];
  constraints?: Record<string, string>;
}

export interface BriefDocument {
  slug: string;
  title: string;
  url?: string;
  content?: string;
}

export interface GenerateSpecResponse {
  status: 'spec_review';
  message: string;
  spec_id: string;
}

export interface StartRunResponse {
  run_id: string;
  status: 'queued';
}

export interface RetryRunResponse {
  run_id: string;
  original_run_id: string;
  status: 'queued';
}

export interface RespondApprovalRequest {
  response: ApprovalResponse;
  response_note?: string;
}

export interface CreatePullRequestRequest {
  approved?: boolean;
}

export interface MergePullRequestRequest {
  merge_method?: 'merge' | 'squash' | 'rebase';
  sha?: string;
}

export interface DeployTaskRequest {
  environment: string;
  ref?: string;
}

export interface DeploymentResponse {
  id: string;
  task_id: string;
  environment: string;
  ref: string;
  provider: string;
  status: string;
  url?: string;
  created_at: string;
}

export interface CreatePolicyRequest {
  name: string;
  resource_type: ResourceType;
  action: Action;
  effect: Effect;
  conditions?: Record<string, unknown>;
  priority?: number;
}

export interface CreateSecretRequest {
  project_id?: string;
  name: string;
  scope: SecretScope;
  description?: string;
  value: string;
}

export interface RotateSecretRequest {
  value: string;
}

export interface IntegrationPayload {
  integration_type: IntegrationType;
  display_name: string;
  config?: Record<string, unknown>;
  token?: string;
  webhook_url?: string;
}

export interface UpdateIntegrationPayload
  extends Partial<Omit<IntegrationPayload, 'integration_type'>> {
  status?: IntegrationStatus;
}

export interface VoiceTaskPayload {
  repository_id: string;
  transcript: string;
  title?: string;
  provider?: string;
  metadata?: Record<string, unknown>;
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

export interface DestroyWorkspaceResponse {
  status: 'destroyed';
  id: string;
}

export interface StatusResponse {
  status: string;
  id?: string;
}
