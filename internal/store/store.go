package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// Store owns the separate writer and maintenance SQLite pools.
type Store struct {
	Writer      *sql.DB
	Maintenance *sql.DB
}

// Open opens the configured SQLite database with the required per-connection pragmas.
func Open(path string) (*Store, error) {
	if err := ensureSecureDatabaseFile(path); err != nil {
		return nil, fmt.Errorf("secure database file: %w", err)
	}

	writer, err := openPool(path)
	if err != nil {
		return nil, err
	}
	maintenance, err := openPool(path)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	store := &Store{Writer: writer, Maintenance: maintenance}
	if err := verifySidecarPermissions(path); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("verify sidecar permissions: %w", err)
	}
	if err := store.verify(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
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

func openPool(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect to sqlite database: %w", err)
	}
	return database, nil
}

func sqliteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "trusted_schema(OFF)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "wal_autocheckpoint(0)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func (store *Store) verify(ctx context.Context) error {
	for name, database := range map[string]*sql.DB{"writer": store.Writer, "maintenance": store.Maintenance} {
		if err := verifyPragmas(ctx, database); err != nil {
			return fmt.Errorf("verify %s database pragmas: %w", name, err)
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

func verifyPragmas(ctx context.Context, database *sql.DB) error {
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		_ = connection.Close()
	}()

	for _, expected := range []struct {
		name  string
		value string
	}{
		{name: "foreign_keys", value: "1"},
		{name: "trusted_schema", value: "0"},
		{name: "journal_mode", value: "wal"},
		{name: "synchronous", value: "2"},
		{name: "wal_autocheckpoint", value: "0"},
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
