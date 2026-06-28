package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRunMigrationsSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrations.db")
	database, err := New("file:" + dbPath)
	if err != nil {
		t.Fatalf("New() sqlite error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(sqlite) error: %v", err)
	}

	for _, table := range []string{
		"agent_messages",
		"agent_runs",
		"agent_steps",
		"approvals",
		"audit_logs",
		"budgets",
		"deployments",
		"detection_results",
		"integrations",
		"model_usage",
		"organizations",
		"policies",
		"project_configs",
		"projects",
		"pull_requests",
		"repositories",
		"review_reports",
		"secret_references",
		"secret_values",
		"task_specs",
		"tasks",
		"users",
		"workspaces",
	} {
		var name string
		if err := database.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migrations: %v", table, err)
		}
	}
}

func TestRunMigrationsPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_DATABASE_URL to run live Postgres migration verification")
	}
	database, err := New(url)
	if err != nil {
		t.Fatalf("New() postgres error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(postgres) error: %v", err)
	}
}

// TestSchemaMigrationDrift verifies that the canonical schema.sql produces the
// same tables, columns, and indexes as applying all Goose migrations.
func TestSchemaMigrationDrift(t *testing.T) {
	schemaDB, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() schema sqlite error: %v", err)
	}
	defer schemaDB.Close()

	// schema.sql intentionally creates tables out of FK dependency order (e.g.
	// workspaces before tasks), so disable foreign keys while applying it.
	if _, err := schemaDB.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys for schema load: %v", err)
	}
	schemaSQL, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	if _, err := schemaDB.Exec(string(schemaSQL)); err != nil {
		t.Fatalf("apply schema.sql: %v", err)
	}
	if _, err := schemaDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("re-enable foreign keys after schema load: %v", err)
	}

	migrationDB, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() migration sqlite error: %v", err)
	}
	defer migrationDB.Close()

	if err := migrationDB.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(sqlite) error: %v", err)
	}

	if diff := schemaDiff(t, schemaDB.DB, migrationDB.DB); diff != "" {
		t.Fatalf("schema.sql drifts from migrations:\n%s", diff)
	}
}

func schemaDiff(t *testing.T, a, b *sql.DB) string {
	t.Helper()

	aTables := listTables(t, a)
	bTables := listTables(t, b)

	var diffs []string
	if missing := setDiff(aTables, bTables); len(missing) > 0 {
		diffs = append(diffs, fmt.Sprintf("tables only in schema.sql: %s", strings.Join(missing, ", ")))
	}
	if missing := setDiff(bTables, aTables); len(missing) > 0 {
		diffs = append(diffs, fmt.Sprintf("tables only in migrations: %s", strings.Join(missing, ", ")))
	}

	common := intersection(aTables, bTables)
	for _, table := range common {
		aCols := listColumns(t, a, table)
		bCols := listColumns(t, b, table)
		if d := stringSliceDiff(aCols, bCols); d != "" {
			diffs = append(diffs, fmt.Sprintf("table %q columns differ: %s", table, d))
		}

		aIdx := listIndexes(t, a, table)
		bIdx := listIndexes(t, b, table)
		if d := stringSliceDiff(aIdx, bIdx); d != "" {
			diffs = append(diffs, fmt.Sprintf("table %q indexes differ: %s", table, d))
		}
	}

	return strings.Join(diffs, "\n")
}

func listTables(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT IN ('sqlite_sequence', 'goose_db_version')
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return tables
}

func listColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()

	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan column info for %s: %v", table, err)
		}
		typ = normalizeType(typ)
		def := ""
		if dfltValue.Valid {
			def = dfltValue.String
		}
		columns = append(columns, fmt.Sprintf("%s|%s|nn=%d|def=%s|pk=%d", name, typ, notNull, def, pk))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %s: %v", table, err)
	}
	sort.Strings(columns)
	return columns
}

func listIndexes(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()

	rows, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'index'
		  AND tbl_name = ?
		  AND name NOT LIKE 'sqlite_autoindex%'
		ORDER BY name
	`, table)
	if err != nil {
		t.Fatalf("list indexes for %s: %v", table, err)
	}
	defer rows.Close()

	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index name for %s: %v", table, err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes for %s: %v", table, err)
	}
	return indexes
}

func normalizeType(typ string) string {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "timestamp" || typ == "timestamptz" {
		return "timestamptz"
	}
	return typ
}

func setDiff(a, b []string) []string {
	var diff []string
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	for _, v := range a {
		if _, ok := set[v]; !ok {
			diff = append(diff, v)
		}
	}
	sort.Strings(diff)
	return diff
}

func intersection(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	var out []string
	seen := make(map[string]struct{})
	for _, v := range a {
		if _, ok := set[v]; ok {
			if _, done := seen[v]; !done {
				seen[v] = struct{}{}
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}

func stringSliceDiff(a, b []string) string {
	if len(a) != len(b) {
		return fmt.Sprintf("len %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Sprintf("%q vs %q at index %d", a[i], b[i], i)
		}
	}
	return ""
}
