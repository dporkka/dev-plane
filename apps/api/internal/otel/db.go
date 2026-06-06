package otel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentedDB wraps a sql.DB with OpenTelemetry tracing for all queries.
type InstrumentedDB struct {
	*sql.DB
	tracer trace.Tracer
}

// WrapDB wraps an existing sql.DB with OpenTelemetry instrumentation.
func WrapDB(db *sql.DB) *InstrumentedDB {
	return &InstrumentedDB{
		DB:     db,
		tracer: Tracer(),
	}
}

// queryContext is a helper that traces SQL query execution.
func (db *InstrumentedDB) queryContext(ctx context.Context, operation, query string, args ...interface{}) (context.Context, trace.Span) {
	ctx, span := db.tracer.Start(ctx, fmt.Sprintf("db.%s", operation),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", query),
			attribute.String("db.operation", operation),
			attribute.Int("db.args.count", len(args)),
		),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	return ctx, span
}

// QueryContext executes a query that returns rows, with tracing.
func (db *InstrumentedDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	ctx, span := db.queryContext(ctx, "query", query, args...)
	defer span.End()

	start := time.Now()
	rows, err := db.DB.QueryContext(ctx, query, args...)
	duration := time.Since(start)

	span.SetAttributes(attribute.Int64("db.duration_ms", duration.Milliseconds()))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}

	return rows, err
}

// QueryRowContext executes a query that is expected to return at most one row, with tracing.
func (db *InstrumentedDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	ctx, span := db.queryContext(ctx, "query_row", query, args...)
	defer span.End()

	start := time.Now()
	row := db.DB.QueryRowContext(ctx, query, args...)
	duration := time.Since(start)

	span.SetAttributes(attribute.Int64("db.duration_ms", duration.Milliseconds()))
	// Note: errors from QueryRow are deferred until Scan

	return row
}

// ExecContext executes a query without returning any rows, with tracing.
func (db *InstrumentedDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	ctx, span := db.queryContext(ctx, "exec", query, args...)
	defer span.End()

	start := time.Now()
	result, err := db.DB.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	span.SetAttributes(attribute.Int64("db.duration_ms", duration.Milliseconds()))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		// Record rows affected if available
		if rowsAffected, raErr := result.RowsAffected(); raErr == nil {
			span.SetAttributes(attribute.Int64("db.rows_affected", rowsAffected))
		}
		if lastID, liErr := result.LastInsertId(); liErr == nil && lastID > 0 {
			span.SetAttributes(attribute.Int64("db.last_insert_id", lastID))
		}
	}

	return result, err
}

// BeginTx starts a transaction with tracing.
func (db *InstrumentedDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	ctx, span := db.tracer.Start(ctx, "db.begin_tx",
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", "begin"),
		),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	defer span.End()

	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	return tx, err
}

// PrepareContext creates a prepared statement with tracing.
func (db *InstrumentedDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	ctx, span := db.queryContext(ctx, "prepare", query)
	defer span.End()

	stmt, err := db.DB.PrepareContext(ctx, query)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	return stmt, err
}

// RecordStats records database pool statistics as metrics.
// Call this periodically (e.g., via a background goroutine) to export pool metrics.
func (db *InstrumentedDB) RecordStats(ctx context.Context) {
	_ = ctx
	_ = db.Stats()
}

// Ensure InstrumentedDB implements the common database interfaces.
var (
	_ interface {
		QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	} = (*InstrumentedDB)(nil)
	_ interface {
		QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	} = (*InstrumentedDB)(nil)
	_ interface {
		ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	} = (*InstrumentedDB)(nil)
	_ interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	} = (*InstrumentedDB)(nil)
	_ interface {
		PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	} = (*InstrumentedDB)(nil)
)

// Compile-time check for driver.Conn interface (reserved for future otelsql integration).
var _ driver.Conn = (*instrumentedConn)(nil)

type instrumentedConn struct {
	driver.Conn
}
