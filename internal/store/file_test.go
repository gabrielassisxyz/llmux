package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileSecurity_NewDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	err := ensureSecureDatabaseFile(dbPath)
	if err != nil {
		t.Fatalf("expected success on new database creation, got: %v", err)
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("failed to stat created db: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %v", info.Mode().Perm())
	}
}

func TestFileSecurity_RejectsSymlink(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "target.db")
	symlinkPath := filepath.Join(tempDir, "link.db")

	if err := os.WriteFile(targetPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatal(err)
	}

	err := ensureSecureDatabaseFile(symlinkPath)
	if err == nil || err.Error() != "database file is a symlink" {
		t.Errorf("expected symlink rejection, got: %v", err)
	}
}

func TestFileSecurity_RejectsInsecureFile(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	if err := os.WriteFile(dbPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	err := ensureSecureDatabaseFile(dbPath)
	if err == nil || err.Error() != "database file has insecure permissions: -rw-r--r--" {
		t.Errorf("expected insecure permissions rejection, got: %v", err)
	}
}

func TestFileSecurity_RejectsNonAbsolute(t *testing.T) {
	err := ensureSecureDatabaseFile("relative/path.db")
	if err == nil {
		t.Errorf("expected non-absolute path rejection, got nil")
	}
}

func TestFileSecurity_OpenVerifiesAppend(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	// Pre-create and set to read-only
	if err := os.WriteFile(dbPath, nil, 0400); err != nil {
		t.Fatal(err)
	}

	_, err := Open(dbPath)
	if err == nil {
		t.Errorf("expected failure opening read-only store")
	}
}

func TestFileSecurity_SidecarPermissions(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	if err := os.WriteFile(dbPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	walPath := dbPath + "-wal"
	if err := os.WriteFile(walPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	err := verifySidecarPermissions(dbPath)
	if err == nil {
		t.Errorf("expected verifySidecarPermissions to fail due to insecure sidecar permissions")
	}
}

func TestFileSecurity_CreationFailureLeavesFileInPlace(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	if err := os.WriteFile(dbPath, nil, 0400); err != nil {
		t.Fatal(err)
	}

	_, err := Open(dbPath)
	if err == nil {
		t.Fatalf("expected Open to fail")
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected dbPath to remain, got %v", err)
	}
}
