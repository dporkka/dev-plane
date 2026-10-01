package db

import (
	"context"
	"database/sql"
	"errors"
)

// TaskCapsuleSQLStore exposes capsule persistence over the raw *sql.DB retained
// by worker handlers. The capsule SQL uses PostgreSQL-style positional
// placeholders, which are accepted by both supported engines (PostgreSQL and
// go-sqlite3), so this adapter does not need to recover the original DB wrapper.
type TaskCapsuleSQLStore struct {
	db *sql.DB
}

func NewTaskCapsuleSQLStore(database *sql.DB) *TaskCapsuleSQLStore {
	return &TaskCapsuleSQLStore{db: database}
}

func (s *TaskCapsuleSQLStore) UpsertTaskCapsule(
	ctx context.Context,
	record TaskCapsuleRecord,
	leases []TaskLeaseRecord,
) error {
	if s == nil || s.db == nil {
		return errors.New("task capsule sql store database is required")
	}
	wrapped := &DB{DB: s.db, Driver: "postgres"}
	return wrapped.UpsertTaskCapsule(ctx, record, leases)
}

func (s *TaskCapsuleSQLStore) LoadTaskCapsule(
	ctx context.Context,
	agentRunID string,
) (TaskCapsuleRecord, []TaskLeaseRecord, error) {
	if s == nil || s.db == nil {
		return TaskCapsuleRecord{}, nil, errors.New("task capsule sql store database is required")
	}
	wrapped := &DB{DB: s.db, Driver: "postgres"}
	return wrapped.LoadTaskCapsule(ctx, agentRunID)
}
