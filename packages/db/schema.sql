-- AI Dev Control Plane — Complete Database Schema
-- Portable SQL compatible with both SQLite and Postgres
-- UUIDs generated in application layer (no gen_random_uuid() dependency)
-- JSON columns: use JSONB for Postgres, TEXT for SQLite (handled by adapter layer)
-- Timestamps: use TIMESTAMPTZ for Postgres, handled via adapter for SQLite

-- =====================================================
-- 1. organizations
-- =====================================================
CREATE TABLE IF NOT EXISTS organizations (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    plan        TEXT NOT NULL DEFAULT 'free',
    settings    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_organizations_slug ON organizations(slug);
CREATE INDEX IF NOT EXISTS idx_organizations_deleted_at ON organizations(deleted_at);

-- =====================================================
-- 2. users
-- =====================================================
CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    email           TEXT NOT NULL,
    name            TEXT,
    avatar_url      TEXT,
    role            TEXT NOT NULL DEFAULT 'member',
    github_id       TEXT,
    github_username TEXT,
    settings        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMPTZ,
    UNIQUE(organization_id, email)
);

CREATE INDEX IF NOT EXISTS idx_users_organization_id ON users(organization_id);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_github_id ON users(github_id);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);

-- =====================================================
-- 3. projects
-- =====================================================
CREATE TABLE IF NOT EXISTS projects (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    description     TEXT,
    settings        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMPTZ,
    UNIQUE(organization_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_projects_organization_id ON projects(organization_id);
CREATE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects(deleted_at);

-- =====================================================
-- 4. repositories
-- =====================================================
CREATE TABLE IF NOT EXISTS repositories (
    id                  UUID PRIMARY KEY,
    project_id          UUID NOT NULL REFERENCES projects(id),
    github_id           BIGINT,
    owner               TEXT NOT NULL,
    name                TEXT NOT NULL,
    full_name           TEXT NOT NULL,
    clone_url           TEXT NOT NULL,
    default_branch      TEXT NOT NULL DEFAULT 'main',
    private             BOOLEAN NOT NULL DEFAULT false,
    connection_status   TEXT NOT NULL DEFAULT 'pending',
    last_synced_at      TIMESTAMPTZ,
    webhook_secret      TEXT,
    settings            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          TIMESTAMPTZ,
    UNIQUE(project_id, full_name)
);

CREATE INDEX IF NOT EXISTS idx_repositories_project_id ON repositories(project_id);
CREATE INDEX IF NOT EXISTS idx_repositories_full_name ON repositories(full_name);
CREATE INDEX IF NOT EXISTS idx_repositories_connection_status ON repositories(connection_status);
CREATE INDEX IF NOT EXISTS idx_repositories_deleted_at ON repositories(deleted_at);

-- =====================================================
-- 5. workspaces
-- =====================================================
CREATE TABLE IF NOT EXISTS workspaces (
    id                  UUID PRIMARY KEY,
    repository_id       UUID NOT NULL REFERENCES repositories(id),
    task_id             UUID REFERENCES tasks(id),
    name                TEXT NOT NULL,
    branch              TEXT NOT NULL,
    base_branch         TEXT NOT NULL DEFAULT 'main',
    worktree_path       TEXT,
    runtime_provider    TEXT NOT NULL DEFAULT 'docker',
    runtime_session_id  TEXT,
    status              TEXT NOT NULL DEFAULT 'pending',
    preview_url         TEXT,
    settings            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_workspaces_repository_id ON workspaces(repository_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_task_id ON workspaces(task_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_status ON workspaces(status);
CREATE INDEX IF NOT EXISTS idx_workspaces_deleted_at ON workspaces(deleted_at);

-- =====================================================
-- 6. tasks
-- =====================================================
CREATE TABLE IF NOT EXISTS tasks (
    id                      UUID PRIMARY KEY,
    project_id              UUID NOT NULL REFERENCES projects(id),
    repository_id           UUID NOT NULL REFERENCES repositories(id),
    workspace_id            UUID REFERENCES workspaces(id),
    created_by              UUID NOT NULL REFERENCES users(id),
    source                  TEXT NOT NULL DEFAULT 'web',
    source_id               TEXT,
    title                   TEXT NOT NULL,
    description             TEXT,
    status                  TEXT NOT NULL DEFAULT 'backlog',
    priority                TEXT NOT NULL DEFAULT 'medium',
    risk_level              TEXT NOT NULL DEFAULT 'low',
    target_branch           TEXT NOT NULL DEFAULT 'main',
    spec                    JSONB,
    acceptance_criteria     JSONB DEFAULT '[]',
    max_cost                DECIMAL(10,4),
    max_runtime_minutes     INTEGER DEFAULT 60,
    approval_requirements   JSONB DEFAULT '[]',
    metadata                JSONB DEFAULT '{}',
    started_at              TIMESTAMPTZ,
    completed_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at              TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_repository_id ON tasks(repository_id);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id ON tasks(workspace_id);
CREATE INDEX IF NOT EXISTS idx_tasks_created_by ON tasks(created_by);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority);
CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_deleted_at ON tasks(deleted_at);

-- =====================================================
-- 7. agent_runs
-- =====================================================
CREATE TABLE IF NOT EXISTS agent_runs (
    id                  UUID PRIMARY KEY,
    task_id             UUID NOT NULL REFERENCES tasks(id),
    workspace_id        UUID REFERENCES workspaces(id),
    agent_role          TEXT NOT NULL DEFAULT 'implementer',
    model               TEXT,
    provider            TEXT,
    status              TEXT NOT NULL DEFAULT 'pending',
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    prompt_tokens       INTEGER DEFAULT 0,
    completion_tokens   INTEGER DEFAULT 0,
    total_cost          DECIMAL(10,6) DEFAULT 0,
    error_message       TEXT,
    summary             TEXT,
    metadata            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_task_id ON agent_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_workspace_id ON agent_runs(workspace_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status);
CREATE INDEX IF NOT EXISTS idx_agent_runs_created_at ON agent_runs(created_at);

-- =====================================================
-- 8. agent_steps
-- =====================================================
CREATE TABLE IF NOT EXISTS agent_steps (
    id              UUID PRIMARY KEY,
    agent_run_id    UUID NOT NULL REFERENCES agent_runs(id),
    step_number     INTEGER NOT NULL,
    step_type       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    content         TEXT,
    tool_name       TEXT,
    tool_input      JSONB,
    tool_output     JSONB,
    command         TEXT,
    command_output  TEXT,
    exit_code       INTEGER,
    file_path       TEXT,
    diff            TEXT,
    cost            DECIMAL(10,6) DEFAULT 0,
    latency_ms      INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_steps_agent_run_id ON agent_steps(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_steps_step_number ON agent_steps(agent_run_id, step_number);
CREATE INDEX IF NOT EXISTS idx_agent_steps_status ON agent_steps(status);
CREATE INDEX IF NOT EXISTS idx_agent_steps_created_at ON agent_steps(created_at);

-- =====================================================
-- 8a. review_reports
-- =====================================================
CREATE TABLE IF NOT EXISTS review_reports (
    id              UUID PRIMARY KEY,
    run_id          UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    summary         TEXT NOT NULL,
    findings        JSONB DEFAULT '[]',
    risk_level      TEXT NOT NULL,
    approvable      BOOLEAN NOT NULL DEFAULT false,
    suggestions     JSONB DEFAULT '[]',
    test_coverage   TEXT NOT NULL DEFAULT '',
    security_notes  TEXT NOT NULL DEFAULT '',
    diff_summary    JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_review_reports_run_id ON review_reports(run_id);
CREATE INDEX IF NOT EXISTS idx_review_reports_risk_level ON review_reports(risk_level);
CREATE INDEX IF NOT EXISTS idx_review_reports_created_at ON review_reports(created_at);

-- =====================================================
-- 9. approvals
-- =====================================================
CREATE TABLE IF NOT EXISTS approvals (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id),
    agent_run_id    UUID REFERENCES agent_runs(id),
    approval_type   TEXT NOT NULL,
    requested_by    UUID NOT NULL REFERENCES users(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    responded_by    UUID REFERENCES users(id),
    response        TEXT,
    response_note   TEXT,
    responded_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_approvals_task_id ON approvals(task_id);
CREATE INDEX IF NOT EXISTS idx_approvals_agent_run_id ON approvals(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_approvals_requested_by ON approvals(requested_by);
CREATE INDEX IF NOT EXISTS idx_approvals_response ON approvals(response);
CREATE INDEX IF NOT EXISTS idx_approvals_created_at ON approvals(created_at);

-- =====================================================
-- 10. policies
-- =====================================================
CREATE TABLE IF NOT EXISTS policies (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    project_id      UUID REFERENCES projects(id),
    name            TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    action          TEXT NOT NULL,
    effect          TEXT NOT NULL,
    conditions      JSONB DEFAULT '{}',
    priority        INTEGER DEFAULT 100,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_policies_organization_id ON policies(organization_id);
CREATE INDEX IF NOT EXISTS idx_policies_project_id ON policies(project_id);
CREATE INDEX IF NOT EXISTS idx_policies_resource_type ON policies(resource_type);
CREATE INDEX IF NOT EXISTS idx_policies_effect ON policies(effect);
CREATE INDEX IF NOT EXISTS idx_policies_created_at ON policies(created_at);

-- =====================================================
-- 11. audit_logs
-- =====================================================
CREATE TABLE IF NOT EXISTS audit_logs (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    actor_type      TEXT NOT NULL,
    actor_id        UUID,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     UUID,
    details         JSONB DEFAULT '{}',
    ip_address      TEXT,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_organization_id ON audit_logs(organization_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_id ON audit_logs(actor_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_type ON audit_logs(resource_type);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);

-- =====================================================
-- 12. model_usage
-- =====================================================
CREATE TABLE IF NOT EXISTS model_usage (
    id                  UUID PRIMARY KEY,
    agent_run_id        UUID REFERENCES agent_runs(id),
    task_id             UUID NOT NULL REFERENCES tasks(id),
    model               TEXT NOT NULL,
    provider            TEXT NOT NULL,
    prompt_tokens       INTEGER NOT NULL DEFAULT 0,
    completion_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tokens        INTEGER NOT NULL DEFAULT 0,
    cost                DECIMAL(10,6) NOT NULL DEFAULT 0,
    latency_ms          INTEGER DEFAULT 0,
    success             BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_model_usage_agent_run_id ON model_usage(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_task_id ON model_usage(task_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usage(model);
CREATE INDEX IF NOT EXISTS idx_model_usage_created_at ON model_usage(created_at);

-- =====================================================
-- 13. agent_messages
-- =====================================================
CREATE TABLE IF NOT EXISTS agent_messages (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id    UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    from_agent      TEXT NOT NULL,
    to_agent        TEXT NOT NULL,
    message_type    TEXT NOT NULL,
    content         TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}',
    consumed_at     TIMESTAMPTZ,
    consumed_by_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_task_id ON agent_messages(task_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_run_id ON agent_messages(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_to_agent ON agent_messages(to_agent);
CREATE INDEX IF NOT EXISTS idx_agent_messages_type ON agent_messages(message_type);
CREATE INDEX IF NOT EXISTS idx_agent_messages_consumed_at ON agent_messages(consumed_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_consumed_by_run_id ON agent_messages(consumed_by_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_created_at ON agent_messages(created_at);

-- =====================================================
-- 14. integrations
-- =====================================================
CREATE TABLE IF NOT EXISTS integrations (
    id                  UUID PRIMARY KEY,
    organization_id     UUID NOT NULL REFERENCES organizations(id),
    integration_type    TEXT NOT NULL,
    display_name        TEXT NOT NULL,
    config              JSONB DEFAULT '{}',
    credentials_encrypted TEXT,
    status              TEXT NOT NULL DEFAULT 'pending',
    webhook_url         TEXT,
    last_synced_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          TIMESTAMPTZ,
    UNIQUE(organization_id, integration_type)
);

CREATE INDEX IF NOT EXISTS idx_integrations_organization_id ON integrations(organization_id);
CREATE INDEX IF NOT EXISTS idx_integrations_status ON integrations(status);
CREATE INDEX IF NOT EXISTS idx_integrations_deleted_at ON integrations(deleted_at);

-- =====================================================
-- 15. secret storage
-- =====================================================
CREATE TABLE IF NOT EXISTS secret_references (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    project_id      UUID REFERENCES projects(id),
    name            TEXT NOT NULL,
    scope           TEXT NOT NULL,
    provider        TEXT NOT NULL,
    key_path        TEXT NOT NULL,
    description     TEXT,
    last_rotated_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secret_refs_org_project_name_scope
    ON secret_references(organization_id, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), name, scope)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_secret_refs_org ON secret_references(organization_id);
CREATE INDEX IF NOT EXISTS idx_secret_refs_project ON secret_references(project_id);
CREATE INDEX IF NOT EXISTS idx_secret_refs_scope ON secret_references(scope);
CREATE INDEX IF NOT EXISTS idx_secret_refs_deleted_at ON secret_references(deleted_at);

CREATE TABLE IF NOT EXISTS secret_values (
    id                  UUID PRIMARY KEY,
    secret_reference_id UUID NOT NULL REFERENCES secret_references(id) ON DELETE CASCADE,
    version             INTEGER NOT NULL,
    key_id              TEXT NOT NULL,
    ciphertext          TEXT NOT NULL,
    active              BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at          TIMESTAMPTZ,
    UNIQUE(secret_reference_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secret_values_active
    ON secret_values(secret_reference_id)
    WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_secret_values_reference ON secret_values(secret_reference_id);
CREATE INDEX IF NOT EXISTS idx_secret_values_key_id ON secret_values(key_id);
