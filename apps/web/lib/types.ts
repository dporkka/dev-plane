// Task types
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
  spec?: any;
  acceptance_criteria?: any[];
  max_cost?: number;
  max_runtime_minutes: number;
  approval_requirements?: any[];
  metadata?: Record<string, any>;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// Agent run types
export type AgentRole =
  | 'planner'
  | 'implementer'
  | 'reviewer'
  | 'test_runner'
  | 'security_reviewer'
  | 'docs_writer'
  | 'release_manager';

export type RunStatus = 'pending' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';

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
  metadata?: Record<string, any>;
  created_at: string;
  updated_at: string;
}

// Agent step types
export type StepType =
  | 'thought'
  | 'tool_call'
  | 'command_run'
  | 'file_patch'
  | 'approval_request'
  | 'message'
  | 'error';

export type StepStatus = 'pending' | 'running' | 'completed' | 'failed';

export interface AgentStep {
  id: string;
  agent_run_id: string;
  step_number: number;
  step_type: StepType;
  status: StepStatus;
  content?: string;
  tool_name?: string;
  tool_input?: any;
  tool_output?: any;
  command?: string;
  command_output?: string;
  exit_code?: number;
  file_path?: string;
  diff?: string;
  cost: number;
  latency_ms: number;
  created_at: string;
}

// Project types
export interface Project {
  id: string;
  organization_id: string;
  name: string;
  slug: string;
  description?: string;
  settings?: Record<string, any>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// Repository types
export type ConnectionStatus = 'pending' | 'connected' | 'error';

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
  settings?: Record<string, any>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// User types
export type UserRole = 'owner' | 'admin' | 'member';

export interface User {
  id: string;
  organization_id: string;
  email: string;
  name?: string;
  avatar_url?: string;
  role: UserRole;
  github_id?: string;
  github_username?: string;
  settings?: Record<string, any>;
  created_at: string;
  updated_at: string;
}

// Organization types
export type Plan = 'free' | 'pro' | 'enterprise';

export interface Organization {
  id: string;
  name: string;
  slug: string;
  plan: Plan;
  settings?: Record<string, any>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// Approval types
export type ApprovalType = 'spec' | 'execution' | 'deploy' | 'risky_action' | 'pr_create';
export type ApprovalResponse = 'approved' | 'rejected';

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
  metadata?: Record<string, any>;
  created_at: string;
  updated_at: string;
}

// Policy types
export type ResourceType = 'file' | 'command' | 'secret' | 'deploy' | 'git' | 'network';
export type Action = 'read' | 'write' | 'execute' | 'delete';
export type Effect = 'allow' | 'ask' | 'deny' | 'admin_only';

export interface Policy {
  id: string;
  organization_id: string;
  project_id?: string;
  name: string;
  resource_type: ResourceType;
  action: Action;
  effect: Effect;
  conditions?: Record<string, any>;
  priority: number;
  created_at: string;
  updated_at: string;
}

// Integration types
export type IntegrationType = 'github' | 'linear' | 'slack' | 'discord';
export type IntegrationStatus = 'pending' | 'connected' | 'error' | 'disconnected';

export interface Integration {
  id: string;
  organization_id: string;
  integration_type: IntegrationType;
  display_name: string;
  config?: Record<string, any>;
  status: IntegrationStatus;
  webhook_url?: string;
  last_synced_at?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// Workspace types
export type WorkspaceStatus =
  | 'pending'
  | 'preparing'
  | 'ready'
  | 'running'
  | 'stopped'
  | 'error'
  | 'destroyed';

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
  settings?: Record<string, any>;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// Dashboard types
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
  recent_failures: Task[];
}

// Spec types
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
}

// Project config types
export interface ProjectConfig {
  id: string;
  repository_id: string;
  package_manager: string;
  framework: string;
  test_command: string;
  lint_command: string;
  typecheck_command: string;
  dev_command: string;
  build_command: string;
}

// Review report types
export interface ReviewReport {
  run_id: string;
  summary: string;
  findings: ReviewFinding[];
  risk_level: string;
  approvable: boolean;
  suggestions: string[];
}

export interface ReviewFinding {
  severity: string;
  file: string;
  line: number;
  message: string;
  category: string;
}

// Pull Request types
export interface PullRequest {
  id: string;
  task_id: string;
  repository_id: string;
  number: number;
  title: string;
  branch: string;
  base_branch: string;
  url: string;
  status: 'open' | 'merged' | 'closed';
  created_at: string;
  updated_at: string;
}

// API response types
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
