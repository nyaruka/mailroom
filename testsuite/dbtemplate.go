package testsuite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/vinovest/sqlx"
)

// Each test gets its own database, cloned from a template built once per test dump. Cloning is a file copy
// inside Postgres and takes ~50ms, whereas restoring the dump takes ~1s - and because no test ever inherits
// another's database, there's no reset step to get wrong.

const (
	// maintenance database we connect to in order to create and drop test databases
	dbAdminDSN = "postgres://mailroom_test:temba@postgres/postgres?sslmode=disable"

	// DSN format for a per-test database
	dbTestDSNFormat = "postgres://mailroom_test:temba@postgres/%s?sslmode=disable&Timezone=UTC"

	dbTestPrefix     = "mailroom_test_t"
	dbTemplatePrefix = "mailroom_test_tpl_"

	// advisory lock key which serializes template creation across test binaries
	dbTemplateLockID = 0x6d61696c
)

var dbCounter atomic.Int64

// admin pool is opened once per test binary and left open for its lifetime
var dbAdmin = sync.OnceValues(func() (*sqlx.DB, error) {
	db, err := sqlx.Open("postgres", dbAdminDSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	return db, db.Ping()
})

// only check the template exists once per test binary - after that we know it's there
var dbTemplate = sync.OnceValues(func() (string, error) {
	admin, err := dbAdmin()
	if err != nil {
		return "", err
	}
	name, err := templateName()
	if err != nil {
		return "", err
	}
	return name, ensureTemplate(context.Background(), admin, name)
})

// templateName is keyed by the content hash of the dump, so regenerating it automatically builds a new
// template and leaves the old one to be swept.
func templateName() (string, error) {
	d, err := os.ReadFile(testdataPath("postgres.dump"))
	if err != nil {
		return "", fmt.Errorf("error reading test dump: %w", err)
	}

	sum := sha256.Sum256(d)

	return fmt.Sprintf("%s%x", dbTemplatePrefix, sum[:6]), nil
}

// ensureTemplate creates the template database if it doesn't already exist. Safe to call concurrently from
// multiple test binaries as `go test ./...` runs each package as its own process.
func ensureTemplate(ctx context.Context, admin *sqlx.DB, name string) error {
	// advisory locks are session scoped so we need to pin ourselves to a single connection
	conn, err := admin.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("error taking admin connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, dbTemplateLockID); err != nil {
		return fmt.Errorf("error taking template lock: %w", err)
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, dbTemplateLockID)

	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("error checking for template %s: %w", name, err)
	}

	if !exists {
		// build under a scratch name and rename, so that a run which dies midway doesn't leave behind a
		// half-restored database that later runs would take for a complete template
		scratch := name + "_wip"

		if err := execDDL(ctx, conn, `DROP DATABASE IF EXISTS %s`, scratch); err != nil {
			return err
		}
		if err := execDDL(ctx, conn, `CREATE DATABASE %s`, scratch); err != nil {
			return err
		}
		if err := restoreDump(ctx, scratch); err != nil {
			return err
		}
		if err := execDDL(ctx, conn, `ALTER DATABASE %s RENAME TO `+pq.QuoteIdentifier(name), scratch); err != nil {
			return err
		}
	}

	return sweepStale(ctx, conn, name)
}

// restoreDump loads the test dump into the given database
func restoreDump(ctx context.Context, dbName string) error {
	dump, err := os.Open(testdataPath("postgres.dump"))
	if err != nil {
		return fmt.Errorf("error opening test dump: %w", err)
	}
	defer dump.Close()

	cmd := exec.CommandContext(ctx, "pg_restore", "-h", "postgres", "-U", "mailroom_test", "-d", dbName, "--no-password")
	cmd.Stdin = dump
	cmd.Env = append(os.Environ(), "PGPASSWORD=temba")

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("error restoring dump into %s: %w: %s", dbName, err, string(output))
	}

	return nil
}

// sweepStale drops templates built from older dumps, and per-test databases orphaned by runs which died
// before they could clean up after themselves.
func sweepStale(ctx context.Context, conn *sql.Conn, keep string) error {
	rows, err := conn.QueryContext(ctx,
		`SELECT datname FROM pg_database WHERE (datname LIKE $1 || '%' OR datname LIKE $2 || '%') AND datname != $3`,
		dbTemplatePrefix, dbTestPrefix, keep,
	)
	if err != nil {
		return fmt.Errorf("error querying for stale databases: %w", err)
	}

	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, name)
	}
	rows.Close()

	for _, name := range stale {
		// another binary may be mid-run and using this database, so don't treat failure as fatal
		terminateConns(ctx, conn, name)
		execDDL(ctx, conn, `DROP DATABASE IF EXISTS %s`, name)
	}

	return nil
}

// createTestDB clones the template into a uniquely named database for a single test, and returns its name.
func createTestDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	tpl, err := dbTemplate()
	require.NoError(t, err, "error ensuring test database template")

	admin, err := dbAdmin()
	require.NoError(t, err)

	name := fmt.Sprintf("%s%d_%d", dbTestPrefix, os.Getpid(), dbCounter.Add(1))

	conn, err := admin.DB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, execDDL(ctx, conn, `DROP DATABASE IF EXISTS %s`, name))
	require.NoError(t, execDDL(ctx, conn, `CREATE DATABASE %s TEMPLATE `+pq.QuoteIdentifier(tpl), name))

	return name
}

// dropTestDB drops a database created by createTestDB
func dropTestDB(t *testing.T, name string) {
	t.Helper()

	ctx := context.Background()

	admin, err := dbAdmin()
	require.NoError(t, err)

	conn, err := admin.DB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	terminateConns(ctx, conn, name)

	require.NoError(t, execDDL(ctx, conn, `DROP DATABASE IF EXISTS %s`, name))
}

// terminateConns kicks off any lingering connections so that the given database can be dropped or cloned
func terminateConns(ctx context.Context, conn *sql.Conn, dbName string) {
	conn.ExecContext(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid != pg_backend_pid()`, dbName,
	)
}

// execDDL runs a DDL statement, quoting the given database name into it as an identifier - which can't be
// done with a bind parameter.
func execDDL(ctx context.Context, conn *sql.Conn, format, dbName string) error {
	stmt := fmt.Sprintf(format, pq.QuoteIdentifier(dbName))

	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("error executing %q: %w", stmt, err)
	}
	return nil
}
