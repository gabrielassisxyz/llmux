package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteDriverWorksWithAnInMemoryDatabase(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database.Close() error = %v", err)
		}
	})

	var value int
	if err := database.QueryRow("SELECT 1").Scan(&value); err != nil {
		t.Fatalf("database.QueryRow().Scan() error = %v", err)
	}
	if value != 1 {
		t.Errorf("query result = %d, want 1", value)
	}
}
