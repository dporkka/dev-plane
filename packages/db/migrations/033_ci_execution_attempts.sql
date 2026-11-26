-- +goose Up
-- +goose StatementBegin
-- Controller-owned lifecycle. Never grant job containers or agents write
-- access. The attempt identity is reserved permanently, including failures.
CREATE TABLE ci_execution_attempts (
    provider TEXT NOT NULL CHECK (provider='dev-plane-ci'),
    repository TEXT NOT NULL,
    pipeline_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    head_sha TEXT NOT NULL CHECK (LENGTH(head_sha) IN (40,64)),
    definition_sha256 TEXT NOT NULL CHECK (LENGTH(definition_sha256)=64),
    state TEXT NOT NULL CHECK (state IN ('queued','assigned','running','terminal')),
    worker_id TEXT,
    lease_hash TEXT,
    queued_ms BIGINT NOT NULL CHECK (queued_ms>0),
    assigned_ms BIGINT,
    started_ms BIGINT,
    finished_ms BIGINT,
    exit_code INTEGER,
    PRIMARY KEY (provider,repository,pipeline_id,run_id,job_id,attempt_id),
    CHECK (assigned_ms IS NULL OR assigned_ms>=queued_ms),
    CHECK (started_ms IS NULL OR (assigned_ms IS NOT NULL AND started_ms>=assigned_ms)),
    CHECK (finished_ms IS NULL OR (started_ms IS NOT NULL AND finished_ms>=started_ms)),
    CHECK (
      (state='queued' AND worker_id IS NULL AND lease_hash IS NULL AND assigned_ms IS NULL AND started_ms IS NULL AND finished_ms IS NULL AND exit_code IS NULL)
      OR (state='assigned' AND worker_id IS NOT NULL AND lease_hash IS NOT NULL AND assigned_ms IS NOT NULL AND started_ms IS NULL AND finished_ms IS NULL AND exit_code IS NULL)
      OR (state='running' AND worker_id IS NOT NULL AND lease_hash IS NOT NULL AND assigned_ms IS NOT NULL AND started_ms IS NOT NULL AND finished_ms IS NULL AND exit_code IS NULL)
      OR (state='terminal' AND worker_id IS NOT NULL AND lease_hash IS NULL AND assigned_ms IS NOT NULL AND started_ms IS NOT NULL AND finished_ms IS NOT NULL AND exit_code IS NOT NULL AND exit_code BETWEEN 0 AND 255)
    )
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE ci_execution_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed=1));
INSERT INTO ci_execution_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM ci_execution_attempts) THEN 0 ELSE 1 END;
DROP TABLE ci_execution_rollback_guard;
DROP TABLE ci_execution_attempts;
-- +goose StatementEnd
