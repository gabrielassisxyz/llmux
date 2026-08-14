package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"sync"

	"github.com/gabrielassisxyz/llmux/internal/policy"
	_ "modernc.org/sqlite"
)

// Store owns the separate writer and maintenance SQLite pools.
type Store struct {
	Writer      *sql.DB
	Maintenance *sql.DB

	checkpointMu        sync.Mutex
	checkpointRunner    checkpointRunner
	walSize             walSizeReader
	terminalCommitCount uint64
	lastCheckpointWAL   int64
}

// Open opens the configured SQLite database with the required per-connection pragmas.
func Open(path string) (*Store, error) {
	ctx, cancel := OperationContext(context.Background())
	defer cancel()

	if err := ensureSecureDatabaseFile(path); err != nil {
		return nil, fmt.Errorf("secure database file: %w", err)
	}

	writer, err := openPool(ctx, path, false)
	if err != nil {
		return nil, err
	}
	maintenance, err := openPool(ctx, path, true)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	store := &Store{Writer: writer, Maintenance: maintenance, checkpointRunner: sqliteCheckpointRunner{database: maintenance}, walSize: fileWALSize{path: path}}
	if err := verifySidecarPermissions(path); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("verify sidecar permissions: %w", err)
	}
	if err := store.verify(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

// Close releases the maintenance pool before the writer pool.
func (store *Store) Close() error {
	maintenanceError := store.Maintenance.Close()
	writerError := store.Writer.Close()
	if maintenanceError != nil {
		return fmt.Errorf("close maintenance database: %w", maintenanceError)
	}
	if writerError != nil {
		return fmt.Errorf("close writer database: %w", writerError)
	}
	return nil
}

func openPool(ctx context.Context, path string, readOnly bool) (*sql.DB, error) {
	database, err := sql.Open("sqlite", sqliteDSN(path, readOnly))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect to sqlite database: %w", err)
	}
	return database, nil
}

// sqliteDSN builds the connection string every physical connection in the
// pool is dialed with. readOnly adds query_only, the pragma that makes the
// maintenance connection's writes-nothing separation an enforced property
// of the connection rather than a fact about which callers happen to use
// it: it refuses INSERT, UPDATE, DELETE and DDL while leaving PRAGMA
// wal_checkpoint, the one operation the maintenance connection runs,
// unaffected.
func sqliteDSN(path string, readOnly bool) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout("+strconv.FormatInt(policy.SQLiteBusyTimeout.Milliseconds(), 10)+")")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "trusted_schema(OFF)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "wal_autocheckpoint(0)")
	if readOnly {
		query.Add("_pragma", "query_only(ON)")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func (store *Store) verify(ctx context.Context) error {
	pools := []struct {
		name     string
		database *sql.DB
		readOnly bool
	}{
		{name: "writer", database: store.Writer, readOnly: false},
		{name: "maintenance", database: store.Maintenance, readOnly: true},
	}
	for _, pool := range pools {
		if err := verifyPragmas(ctx, pool.database, pool.readOnly); err != nil {
			return fmt.Errorf("verify %s database pragmas: %w", pool.name, err)
		}
	}
	if err := store.verifyAppend(ctx); err != nil {
		return fmt.Errorf("verify append permissions: %w", err)
	}
	return nil
}

func (store *Store) verifyAppend(ctx context.Context) error {
	tx, err := store.Writer.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// A read-only database fails on write, but just beginning a deferred transaction
	// doesn't write to the WAL. We must execute something or use BEGIN IMMEDIATE.
	// We'll execute a dummy savepoint.
	if _, err := tx.ExecContext(ctx, "SAVEPOINT verify_append"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("acquire write lock: %w", err)
	}
	_ = tx.Rollback()
	return nil
}

func verifyPragmas(ctx context.Context, database *sql.DB, readOnly bool) error {
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		_ = connection.Close()
	}()

	queryOnly := "0"
	if readOnly {
		queryOnly = "1"
	}
	for _, expected := range []struct {
		name  string
		value string
	}{
		{name: "foreign_keys", value: "1"},
		{name: "busy_timeout", value: strconv.FormatInt(policy.SQLiteBusyTimeout.Milliseconds(), 10)},
		{name: "trusted_schema", value: "0"},
		{name: "journal_mode", value: "wal"},
		{name: "synchronous", value: "2"},
		{name: "wal_autocheckpoint", value: "0"},
		{name: "query_only", value: queryOnly},
	} {
		var actual string
		if err := connection.QueryRowContext(ctx, "PRAGMA "+expected.name).Scan(&actual); err != nil {
			return fmt.Errorf("read %s: %w", expected.name, err)
		}
		if actual != expected.value {
			return fmt.Errorf("%s = %q, want %q", expected.name, actual, expected.value)
		}
	}
	return nil
}
