package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dummyFileInfo struct{}

func (dummyFileInfo) Name() string       { return "dummy" }
func (dummyFileInfo) Size() int64        { return 0 }
func (dummyFileInfo) Mode() os.FileMode  { return 0 }
func (dummyFileInfo) ModTime() time.Time { return time.Time{} }
func (dummyFileInfo) IsDir() bool        { return false }
func (dummyFileInfo) Sys() any           { return nil }

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

func TestFileSecurity_RejectsMissingParentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "missing", "test.db")

	err := ensureSecureDatabaseFile(dbPath)
	if err == nil {
		t.Fatal("expected rejection for missing parent directory, got nil")
	}
	if !strings.Contains(err.Error(), "stat parent directory") {
		t.Errorf("expected error to mention stat parent directory, got: %v", err)
	}
}

func TestFileSecurity_RejectsParentDirNotOwnedByServiceUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership test requires root to create a directory owned by another user")
	}

	tempDir := t.TempDir()
	badDir := filepath.Join(tempDir, "bad-owner")
	if err := os.Mkdir(badDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(badDir, 1, -1); err != nil {
		t.Skipf("could not chown directory to uid 1: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chown(badDir, os.Geteuid(), -1); err != nil {
			t.Logf("failed to restore directory ownership: %v", err)
		}
	})

	dbPath := filepath.Join(badDir, "test.db")
	err := ensureSecureDatabaseFile(dbPath)
	if err == nil || err.Error() != "parent directory not owned by service user" {
		t.Errorf("expected parent directory ownership rejection, got: %v", err)
	}
}

func TestDirectoryOwnedByUID(t *testing.T) {
	tempDir := t.TempDir()
	dirInfo, err := os.Stat(tempDir)
	if err != nil {
		t.Fatalf("stat temp dir: %v", err)
	}

	if !directoryOwnedByUID(dirInfo, os.Geteuid()) {
		t.Error("expected directory to be reported as owned by current uid")
	}
	if directoryOwnedByUID(dirInfo, os.Geteuid()+1) {
		t.Error("expected directory not to be reported as owned by a different uid")
	}
}

func TestDirectoryOwnedByUID_ReportsFalseWhenStatMissing(t *testing.T) {
	dirInfo := dummyFileInfo{}
	if directoryOwnedByUID(dirInfo, os.Geteuid()) {
		t.Error("expected false for a FileInfo whose Sys() is not *syscall.Stat_t")
	}
}

func TestFileSecurity_RejectsParentDirGroupOrOtherWritable(t *testing.T) {
	tempDir := t.TempDir()

	for _, test := range []struct {
		name string
		mode os.FileMode
	}{
		{"group writable only", 0720},
		{"other writable only", 0702},
		{"group and other writable", 0777},
		{"write bits without owner write", 0222},
	} {
		t.Run(test.name, func(t *testing.T) {
			badDir := filepath.Join(tempDir, test.name)
			if err := os.Mkdir(badDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(badDir, test.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(badDir, 0700); err != nil {
					t.Logf("failed to restore directory permissions: %v", err)
				}
			})

			dbPath := filepath.Join(badDir, "test.db")
			err := ensureSecureDatabaseFile(dbPath)
			if err == nil || err.Error() != "parent directory allows group or other write access" {
				t.Errorf("expected parent directory group/other writable rejection for mode %04o, got: %v", test.mode, err)
			}
		})
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
