-- +goose Up
-- +goose StatementBegin
-- Durable device receipts; requires 025_device_enrollment.sql.
CREATE TABLE IF NOT EXISTS device_operations (
  operation_id TEXT PRIMARY KEY CHECK (length(operation_id)=68 AND substr(operation_id,1,4)='dp1_'),
  organization_id UUID NOT NULL REFERENCES organizations(id),
  actor_user_id UUID NOT NULL REFERENCES users(id),
  device_id TEXT NOT NULL REFERENCES enrolled_devices(device_id),
  workspace_id UUID NOT NULL REFERENCES workspaces(id),
  tool_name TEXT NOT NULL CHECK (tool_name IN
    ('workspaces_list','files_list','files_read','files_search','jobs_start','jobs_status','jobs_cancel')),
  arguments_digest TEXT NOT NULL CHECK (length(arguments_digest)=64),
  owner_epoch BIGINT NOT NULL CHECK (owner_epoch>0),
  state TEXT NOT NULL CHECK (state IN ('prepared','dispatched','unknown','completed','failed')),
  result_digest TEXT CHECK (result_digest IS NULL OR length(result_digest)=64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT device_operation_receipt_required CHECK (
    (state IN ('completed','failed') AND result_digest IS NOT NULL)
    OR (state IN ('prepared','dispatched','unknown') AND result_digest IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_device_operations_scope
  ON device_operations(organization_id,actor_user_id,device_id,workspace_id);
CREATE INDEX IF NOT EXISTS idx_device_operations_reconcile
  ON device_operations(state,updated_at) WHERE state IN ('dispatched','unknown');
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_device_operations_reconcile;
DROP INDEX IF EXISTS idx_device_operations_scope;
DROP TABLE IF EXISTS device_operations;
-- +goose StatementEnd
