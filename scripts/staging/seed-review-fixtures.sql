-- Synthetic-only fixture for ephemeral staging QA.
-- NEVER execute against an existing, shared, or production database.
\set ON_ERROR_STOP on

INSERT INTO organizations (id, name, slug)
VALUES
 ('10000000-0000-4000-8000-000000000001', 'QA Alpha Workspace', 'qa-alpha-staging-only'),
 ('10000000-0000-4000-8000-000000000002', 'QA Beta Workspace', 'qa-beta-staging-only');

INSERT INTO users (id, organization_id, email, name, role)
VALUES
 ('20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'alpha@example.invalid', 'Alpha Reviewer', 'admin'),
 ('20000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'beta@example.invalid', 'Beta Reviewer', 'admin');

INSERT INTO projects (id, organization_id, name, slug)
VALUES
 ('30000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'QA Alpha Project', 'qa-alpha-project'),
 ('30000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'QA Beta Project', 'qa-beta-project');

INSERT INTO repositories (id, project_id, owner, name, full_name, clone_url, connection_status)
VALUES
 ('40000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'stage-fixtures', 'alpha', 'stage-fixtures/alpha', 'https://github.com/stage-fixtures/alpha.git', 'connected'),
 ('40000000-0000-4000-8000-000000000002', '30000000-0000-4000-8000-000000000002', 'stage-fixtures', 'beta', 'stage-fixtures/beta', 'https://github.com/stage-fixtures/beta.git', 'connected');

INSERT INTO tasks (id, project_id, repository_id, created_by, title, status)
VALUES
 ('50000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'QA Alpha review task', 'completed'),
 ('50000000-0000-4000-8000-000000000002', '30000000-0000-4000-8000-000000000002', '40000000-0000-4000-8000-000000000002', '20000000-0000-4000-8000-000000000002', 'QA Beta review task', 'completed');

INSERT INTO agent_runs (id, task_id, status, agent_role)
VALUES
 ('60000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001', 'completed', 'reviewer'),
 ('60000000-0000-4000-8000-000000000002', '50000000-0000-4000-8000-000000000002', 'completed', 'reviewer');

INSERT INTO review_reports (id, run_id, summary, findings, risk_level, approvable, suggestions, test_coverage, security_notes, diff_summary)
VALUES
 ('70000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001',
 'QA Alpha persisted review: authorization checks required',
 '[{"severity":"critical","category":"security","file":"qa/alpha.ts","line":12,"message":"QA Alpha security finding","suggestion":"Check tenant boundaries"}]'::jsonb,
 'critical', false, '["Verify organization membership"]'::jsonb, 'QA coverage only', 'Alpha security context', '{"files_changed":1,"insertions":10,"deletions":1}'::jsonb),
 ('70000000-0000-4000-8000-000000000002', '60000000-0000-4000-8000-000000000002',
 'QA Beta persisted review: isolated from Alpha',
 '[{"severity":"high","category":"security","file":"qa/beta.ts","line":22,"message":"QA Beta security finding","suggestion":"Check authorization"}]'::jsonb,
 'high', false, '["Review Beta permission checks"]'::jsonb, 'QA coverage only', 'Beta security context', '{"files_changed":2,"insertions":12,"deletions":2}'::jsonb);

INSERT INTO pull_requests (id, task_id, run_id, repository_id, number, title, body, branch, base_branch, url, state, draft, created_by, created_at, updated_at)
VALUES
 ('80000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001', 101,
 'QA Alpha isolated PR', 'Synthetic Alpha PR', 'qa/alpha-branch', 'main', 'https://github.com/stage-fixtures/alpha/pull/101', 'open', false, '20000000-0000-4000-8000-000000000001', NOW() - INTERVAL '1 day', NOW()),
 ('80000000-0000-4000-8000-000000000002', '50000000-0000-4000-8000-000000000002', '60000000-0000-4000-8000-000000000002', '40000000-0000-4000-8000-000000000002', 202,
 'QA Beta isolated PR', 'Synthetic Beta PR', 'qa/beta-branch', 'main', 'https://github.com/stage-fixtures/beta/pull/202', 'open', false, '20000000-0000-4000-8000-000000000002', NOW() - INTERVAL '1 day', NOW());
