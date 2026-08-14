package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ensureSecureDatabaseFile verifies the parent directory permissions,
// pre-creates the database file with mode 0600 if it does not exist,
// and enforces that existing files are regular, not symlinks, and not readable/writable
// by group or others (0600 or stricter).
func ensureSecureDatabaseFile(dbPath string) error {
	if !filepath.IsAbs(dbPath) {
		return fmt.Errorf("database path must be absolute: %s", dbPath)
	}

	parentDir := filepath.Dir(dbPath)
	dirInfo, err := os.Stat(parentDir)
	if err != nil {
		return fmt.Errorf("stat parent directory: %w", err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("parent path is not a directory: %s", parentDir)
	}

	// Verify parent directory is owned by the current process user and denies write to group/other
	stat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get underlying stat for directory: %s", parentDir)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("parent directory not owned by service user")
	}

	if (dirInfo.Mode().Perm() & 0022) != 0 {
		return fmt.Errorf("parent directory allows group or other write access")
	}

	// Lstat the database file
	fileInfo, err := os.Lstat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Pre-create atomically
			f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				return fmt.Errorf("failed to pre-create database file: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("failed to close pre-created database file: %w", err)
			}
			return nil
		}
		return fmt.Errorf("lstat database file: %w", err)
	}

	// Reject symlink
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("database file is a symlink")
	}

	// Reject non-regular file
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("database file is not a regular file")
	}

	// Reject group/other readable or writable
	if (fileInfo.Mode().Perm() & 0077) != 0 {
		return fmt.Errorf("database file has insecure permissions: %v", fileInfo.Mode().Perm())
	}

	return nil
}

// verifySidecarPermissions rechecks the database and its SQLite sidecars
// (-wal, -shm) to ensure they have secure permissions (0600 or stricter).
func verifySidecarPermissions(dbPath string) error {
	paths := []string{
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
	}

	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("lstat sidecar file %s: %w", p, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sidecar file %s is a symlink", p)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sidecar file %s is not a regular file", p)
		}
		if (info.Mode().Perm() & 0077) != 0 {
			return fmt.Errorf("sidecar file %s has insecure permissions: %v", p, info.Mode().Perm())
		}
	}
	return nil
}
