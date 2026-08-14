package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func overrideMigrations(t *testing.T, mockFS fs.FS) {
	old := migrationFiles
	migrationFiles = mockFS
	t.Cleanup(func() {
		migrationFiles = old
	})
}

func TestHighestSupportedVersion(t *testing.T) {
	mockFS := fstest.MapFS{
		"migrations/001_one.sql":   {Data: []byte("SELECT 1;")},
		"migrations/002_two.sql":   {Data: []byte("SELECT 2;")},
		"migrations/005_three.sql": {Data: []byte("SELECT 3;")},
	}
	overrideMigrations(t, mockFS)

	highest, err := HighestSupportedVersion()
	if err != nil {
		t.Fatalf("HighestSupportedVersion() error = %v", err)
	}
	if highest != 5 {
		t.Errorf("HighestSupportedVersion() = %d, want 5", highest)
	}
}

func TestHighestSupportedVersion_Empty(t *testing.T) {
	overrideMigrations(t, fstest.MapFS{})
	highest, err := HighestSupportedVersion()
	if err != nil {
		t.Fatalf("HighestSupportedVersion() error = %v", err)
	}
	if highest != 0 {
		t.Errorf("HighestSupportedVersion() = %d, want 0", highest)
	}
}

func TestMigrate_SuccessPath(t *testing.T) {
	mockFS := fstest.MapFS{
		"migrations/001_initial.sql": {Data: []byte("CREATE TABLE test_table (id INTEGER PRIMARY KEY);")},
		"migrations/002_data.sql":    {Data: []byte("INSERT INTO test_table (id) VALUES (1);")},
	}
	overrideMigrations(t, mockFS)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	var userVersion int
	if err := s.Writer.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVersion != 2 {
		t.Errorf("user_version = %d, want 2", userVersion)
	}

	var count int
	if err := s.Writer.QueryRow("SELECT COUNT(*) FROM test_table").Scan(&count); err != nil {
		t.Fatalf("query test_table: %v", err)
	}
	if count != 1 {
		t.Errorf("test_table count = %d, want 1", count)
	}
}

func TestMigrate_FutureVersionRefusal(t *testing.T) {
	mockFS := fstest.MapFS{
		"migrations/001_initial.sql": {Data: []byte("CREATE TABLE test_table (id INTEGER);")},
	}
	overrideMigrations(t, mockFS)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Artificially set user_version higher than available migrations
	if _, err := s.Writer.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	err = s.Migrate(context.Background())
	if err == nil {
		t.Fatal("Migrate() expected error for future version, got nil")
	}
	if err.Error() != "database user_version 2 is newer than highest supported version 1" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMigrate_RollsBackOnFailureAndLeavesNoPartialSchema(t *testing.T) {
	mockFS := fstest.MapFS{
		"migrations/001_initial.sql": {Data: []byte("CREATE TABLE valid_table (id INTEGER);")},
		"migrations/002_bad.sql":     {Data: []byte("CREATE TABLE another_table (id INTEGER); INVALID SQL SYNTAX;")},
	}
	overrideMigrations(t, mockFS)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(dbPath)
	if err == nil {
		t.Cleanup(func() { _ = s.Close() })
		t.Fatal("Open() expected error for bad SQL, got nil")
	}

	// Open the database directly to inspect it
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open raw db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Because of rollback, another_table should not exist, and user_version should still be 1
	var userVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVersion != 1 {
		t.Errorf("user_version = %d, want 1 after rollback", userVersion)
	}

	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='another_table'").Scan(&name)
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows for another_table, got %v (name=%q)", err, name)
	}
}
