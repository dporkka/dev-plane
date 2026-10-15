package db

import (
	"context"
	"fmt"
)

// ensureVerificationAttemptGuards prevents ordinary UPDATE and DELETE statements
// from mutating concluded evidence. This does not protect against DB owners,
// superusers, DROP TRIGGER, or credentials that can run arbitrary DDL.
// A privileged migration role and a separately restricted runtime role are still required.
func (db *DB) ensureVerificationAttemptGuards(ctx context.Context) error {
	if db.Driver == "sqlite" {
		for _, query := range []string{
			`CREATE TRIGGER IF NOT EXISTS verification_attempts_no_update
BEFORE UPDATE ON verification_attempts BEGIN
	SELECT RAISE(ABORT, 'verification_attempts is append-only');
END`,
			`CREATE TRIGGER IF NOT EXISTS verification_attempts_no_delete
BEFORE DELETE ON verification_attempts BEGIN
	SELECT RAISE(ABORT, 'verification_attempts is append-only');
END`,
		} {
			if _, err := db.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("install sqlite verification guard: %w", err)
			}
		}
		return nil
	}
	if db.Driver != "postgres" {
		return fmt.Errorf("unsupported verification guard dialect %s", db.Driver)
	}
	// Both the function and trigger are created idempotently for repeated startup.
	const functionSQL = `CREATE OR REPLACE FUNCTION public.reject_verification_attempt_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $guard$
BEGIN
	RAISE EXCEPTION 'verification_attempts is append-only' USING ERRCODE = 'integrity_constraint_violation';
END;
$guard$`
	if _, err := db.ExecContext(ctx, functionSQL); err != nil {
		return fmt.Errorf("install postgres verification guard function: %w", err)
	}
	const triggerSQL = `DO $guard$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgname = 'verification_attempts_no_mutation'
			AND tgrelid = 'verification_attempts'::regclass
			AND NOT tgisinternal
	) THEN
		CREATE TRIGGER verification_attempts_no_mutation
		BEFORE UPDATE OR DELETE ON verification_attempts
		FOR EACH ROW EXECUTE FUNCTION public.reject_verification_attempt_mutation();
	END IF;
END
$guard$`
	if _, err := db.ExecContext(ctx, triggerSQL); err != nil {
		return fmt.Errorf("install postgres verification guard trigger: %w", err)
	}
	return nil
}
