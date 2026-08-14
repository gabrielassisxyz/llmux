package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
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
	assertFreshConnectionPragmas(t, store.Writer)
	assertFreshConnectionPragmas(t, store.Maintenance)
}

func assertFreshConnectionPragmas(t *testing.T, database *sql.DB) {
	t.Helper()
	database.SetMaxIdleConns(0)
	if err := verifyPragmas(context.Background(), database); err != nil {
		t.Fatalf("first connection pragmas: %v", err)
	}
	if err := verifyPragmas(context.Background(), database); err != nil {
		t.Fatalf("fresh connection pragmas: %v", err)
	}
}

func TestOpenRejectsRelativePath(t *testing.T) {
	store, err := Open("llmux.db")
	if err == nil {
		_ = store.Close()
		t.Fatal("Open() error = nil")
	}
}
