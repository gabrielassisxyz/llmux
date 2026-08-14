package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenConfiguresSeparateSingleConnectionPools(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	if store.Writer == store.Maintenance {
		t.Fatal("writer and maintenance pools are the same")
	}
	if store.Writer.Stats().MaxOpenConnections != 1 {
		t.Errorf("writer max connections = %d, want 1", store.Writer.Stats().MaxOpenConnections)
	}
	if store.Maintenance.Stats().MaxOpenConnections != 1 {
		t.Errorf("maintenance max connections = %d, want 1", store.Maintenance.Stats().MaxOpenConnections)
	}
	assertFreshConnectionPragmas(t, store.Writer, false)
	assertFreshConnectionPragmas(t, store.Maintenance, true)
}

func assertFreshConnectionPragmas(t *testing.T, database *sql.DB, readOnly bool) {
	t.Helper()
	database.SetMaxIdleConns(0)
	if err := verifyPragmas(context.Background(), database, readOnly); err != nil {
		t.Fatalf("first connection pragmas: %v", err)
	}
	if err := verifyPragmas(context.Background(), database, readOnly); err != nil {
		t.Fatalf("fresh connection pragmas: %v", err)
	}
}

// TestPragmasHoldOnAConnectionThePoolDidNotHaveAtOpen proves the property
// PLAN.md 15.4 states directly: the required pragmas are per connection,
// not per database, and database/sql may open a new connection at any
// moment. Reading a pragma back once, on the one connection Open already
// dialed, would pass even if the DSN's _pragma mechanism only applied on
// the very first dial. SetConnMaxLifetime forces every connection the pool
// currently holds to be discarded and redialed before it can be reused, so
// each pragma read below runs on a connection Open never saw.
func TestPragmasHoldOnAConnectionThePoolDidNotHaveAtOpen(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	pools := []struct {
		name     string
		database *sql.DB
		readOnly bool
	}{
		{name: "writer", database: store.Writer, readOnly: false},
		{name: "maintenance", database: store.Maintenance, readOnly: true},
	}
	for _, pool := range pools {
		pool.database.SetConnMaxLifetime(time.Nanosecond)
		for i := 0; i < 3; i++ {
			if err := verifyPragmas(context.Background(), pool.database, pool.readOnly); err != nil {
				t.Fatalf("%s: pragmas on redialed connection %d: %v", pool.name, i, err)
			}
		}
	}
}

// TestMaintenanceConnectionRefusesAWrite proves the maintenance pool
// actually rejects a write, rather than merely happening to be used only
// for reads and checkpoints by the rest of the codebase. A SAVEPOINT alone
// does not exercise this: it opens a transaction without changing data, so
// it succeeds under query_only exactly as it would on a writable
// connection. CREATE TABLE is one of the statement kinds query_only is
// documented to refuse, and it needs no schema owned by another bead. The
// checkpoint pragma itself, the one operation the maintenance connection
// legitimately runs, is proven unaffected separately by
// internal/store/checkpoint_test.go.
func TestMaintenanceConnectionRefusesAWrite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	ctx := context.Background()
	if _, err := store.Maintenance.ExecContext(ctx, "CREATE TABLE probe_maintenance_write (x INTEGER)"); err == nil {
		t.Fatal("maintenance connection accepted a CREATE TABLE; it must refuse one")
	}
}

func TestOpenRejectsRelativePath(t *testing.T) {
	store, err := Open("llmux.db")
	if err == nil {
		_ = store.Close()
		t.Fatal("Open() error = nil")
	}
}
