// Package taskgraph owns durable task dependency DAG semantics.
package taskgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSelfDependency  = errors.New("task cannot depend on itself")
	ErrDependencyCycle = errors.New("task dependency would create a cycle")
	ErrCrossRepository = errors.New("task dependencies must stay within one repository")
	ErrTaskNotFound    = errors.New("task not found")
)

type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Blocker struct {
	DependencyTaskID string
	Status           string
}

func AddDependency(ctx context.Context, db *sql.DB, taskID, dependsOnTaskID string) error {
	taskID = strings.TrimSpace(taskID)
	dependsOnTaskID = strings.TrimSpace(dependsOnTaskID)
	if taskID == "" || dependsOnTaskID == "" {
		return fmt.Errorf("task and dependency IDs are required")
	}
	if taskID == dependsOnTaskID {
		return ErrSelfDependency
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dependency transaction: %w", err)
	}
	defer tx.Rollback()

	taskProject, taskRepo, err := loadTaskScope(ctx, tx, taskID)
	if err != nil {
		return err
	}
	depProject, depRepo, err := loadTaskScope(ctx, tx, dependsOnTaskID)
	if err != nil {
		return err
	}
	if taskProject != depProject || taskRepo != depRepo {
		return ErrCrossRepository
	}

	var cycleCount int
	if err := tx.QueryRowContext(ctx, `
		WITH RECURSIVE dependency_chain(id) AS (
			SELECT depends_on_task_id
			FROM task_dependencies
			WHERE task_id = $1
			UNION
			SELECT td.depends_on_task_id
			FROM task_dependencies td
			JOIN dependency_chain dc ON td.task_id = dc.id
		)
		SELECT COUNT(*)
		FROM dependency_chain
		WHERE id = $2
	`, dependsOnTaskID, taskID).Scan(&cycleCount); err != nil {
		return fmt.Errorf("check dependency cycle: %w", err)
	}
	if cycleCount > 0 {
		return ErrDependencyCycle
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_dependencies (task_id, depends_on_task_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (task_id, depends_on_task_id) DO NOTHING
	`, taskID, dependsOnTaskID, time.Now().UTC()); err != nil {
		return fmt.Errorf("insert task dependency: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task dependency: %w", err)
	}
	return nil
}

func ListBlockers(ctx context.Context, q Querier, taskID string) ([]Blocker, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT dependency.id,
		       CASE WHEN dependency.deleted_at IS NOT NULL THEN 'deleted' ELSE dependency.status END
		FROM task_dependencies td
		JOIN tasks dependency ON dependency.id = td.depends_on_task_id
		WHERE td.task_id = $1
		  AND (dependency.deleted_at IS NOT NULL OR dependency.status <> 'done')
		ORDER BY dependency.id
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task dependency blockers: %w", err)
	}
	defer rows.Close()

	var blockers []Blocker
	for rows.Next() {
		var blocker Blocker
		if err := rows.Scan(&blocker.DependencyTaskID, &blocker.Status); err != nil {
			return nil, fmt.Errorf("scan task dependency blocker: %w", err)
		}
		blockers = append(blockers, blocker)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task dependency blockers: %w", err)
	}
	return blockers, nil
}

func EligibleDependents(ctx context.Context, q Querier, completedTaskID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT edge.task_id
		FROM task_dependencies edge
		JOIN tasks dependent ON dependent.id = edge.task_id
		WHERE edge.depends_on_task_id = $1
		  AND dependent.deleted_at IS NULL
		  AND dependent.status = 'approved'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM task_dependencies sibling
		      LEFT JOIN tasks dependency ON dependency.id = sibling.depends_on_task_id
		      WHERE sibling.task_id = edge.task_id
		        AND (
		            dependency.id IS NULL
		            OR dependency.deleted_at IS NOT NULL
		            OR dependency.status <> 'done'
		        )
		  )
		ORDER BY edge.task_id
	`, completedTaskID)
	if err != nil {
		return nil, fmt.Errorf("list eligible dependent tasks: %w", err)
	}
	defer rows.Close()

	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, fmt.Errorf("scan eligible dependent task: %w", err)
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate eligible dependent tasks: %w", err)
	}
	return taskIDs, nil
}

func loadTaskScope(ctx context.Context, q Querier, taskID string) (projectID, repositoryID string, err error) {
	var deletedAt sql.NullTime
	err = q.QueryRowContext(ctx, `
		SELECT project_id, repository_id, deleted_at
		FROM tasks
		WHERE id = $1
	`, taskID).Scan(&projectID, &repositoryID, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return "", "", fmt.Errorf("load task scope: %w", err)
	}
	if deletedAt.Valid {
		return "", "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	return projectID, repositoryID, nil
}
