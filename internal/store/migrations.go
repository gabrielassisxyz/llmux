package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS
var migrationFiles fs.FS = embeddedMigrations

type migration struct {
	version int
	name    string
	content string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid migration filename format: %s", entry.Name())
		}

		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid migration version %q in %s", parts[0], entry.Name())
		}

		content, err := fs.ReadFile(migrationFiles, path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{
			version: version,
			name:    entry.Name(),
			content: string(content),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for i := 1; i < len(migrations); i++ {
		if migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("duplicate migration version: %d", migrations[i].version)
		}
	}

	return migrations, nil
}

// HighestSupportedVersion returns the maximum user_version that this build knows how to apply.
func HighestSupportedVersion() (int, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(migrations) == 0 {
		return 0, nil
	}
	return migrations[len(migrations)-1].version, nil
}

// Migrate applies all pending embedded migrations sequentially to the database.
// It applies them using the store's Writer connection.
func (s *Store) Migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	// In SQLite, user_version is a connection-agnostic PRAGMA stored in the database file.
	var userVersion int
	if err := s.Writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	highest := 0
	if len(migrations) > 0 {
		highest = migrations[len(migrations)-1].version
	}

	if userVersion > highest {
		return fmt.Errorf("database user_version %d is newer than highest supported version %d", userVersion, highest)
	}

	for _, m := range migrations {
		if m.version <= userVersion {
			continue
		}

		if err := applyMigration(ctx, s.Writer, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}

	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.content); err != nil {
		return fmt.Errorf("execute statements: %w", err)
	}

	// PRAGMA user_version cannot use parameterized queries, must be formatted.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return fmt.Errorf("update user_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
