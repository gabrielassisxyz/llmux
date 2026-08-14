package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestInsertProcessEventsPersistSchemaVersionAndRejectBadRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	start := ProcessStartEvent{
		RecordID:          "start-record",
		ProcessInstanceID: "process-1",
		AtUS:              100,
		Version:           "v1.2.3",
		Revision:          "abcdef",
	}
	if err := store.InsertProcessStart(context.Background(), start); err != nil {
		t.Fatalf("InsertProcessStart() error = %v", err)
	}

	var schemaVersion int
	if err := store.Writer.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	var startVersion, startRevision string
	var storedSchemaVersion int
	var elapsed sql.NullInt64
	if err := store.Writer.QueryRowContext(context.Background(), `
		SELECT version, revision, schema_version, process_elapsed_us
		FROM process_event
		WHERE record_id = ?
	`, start.RecordID).Scan(&startVersion, &startRevision, &storedSchemaVersion, &elapsed); err != nil {
		t.Fatalf("query inserted process start: %v", err)
	}
	if startVersion != start.Version || startRevision != start.Revision {
		t.Errorf("process start build identity = (%q, %q), want (%q, %q)", startVersion, startRevision, start.Version, start.Revision)
	}
	if storedSchemaVersion != schemaVersion {
		t.Errorf("schema_version = %d, want current user_version %d", storedSchemaVersion, schemaVersion)
	}
	if elapsed.Valid {
		t.Errorf("process start elapsed = %d, want NULL", elapsed.Int64)
	}

	stop := ProcessStopEvent{
		RecordID:          "stop-record",
		ProcessInstanceID: start.ProcessInstanceID,
		AtUS:              300,
		ProcessElapsedUS:  200,
		Version:           start.Version,
		Revision:          start.Revision,
	}
	if err := store.InsertProcessStop(context.Background(), stop); err != nil {
		t.Fatalf("InsertProcessStop() error = %v", err)
	}
	var stopVersion, stopRevision string
	var stopSchemaVersion int
	var stopElapsed int64
	if err := store.Writer.QueryRowContext(context.Background(), `
		SELECT version, revision, schema_version, process_elapsed_us
		FROM process_event
		WHERE record_id = ?
	`, stop.RecordID).Scan(&stopVersion, &stopRevision, &stopSchemaVersion, &stopElapsed); err != nil {
		t.Fatalf("query inserted process stop: %v", err)
	}
	if stopVersion != stop.Version || stopRevision != stop.Revision {
		t.Errorf("process stop build identity = (%q, %q), want (%q, %q)", stopVersion, stopRevision, stop.Version, stop.Revision)
	}
	if stopSchemaVersion != schemaVersion {
		t.Errorf("process stop schema_version = %d, want current user_version %d", stopSchemaVersion, schemaVersion)
	}
	if stopElapsed != stop.ProcessElapsedUS {
		t.Errorf("process stop elapsed = %d, want %d", stopElapsed, stop.ProcessElapsedUS)
	}

	duplicateStart := start
	duplicateStart.RecordID = "duplicate-start-record"
	if err := store.InsertProcessStart(context.Background(), duplicateStart); err == nil {
		t.Fatal("InsertProcessStart() error = nil for duplicate process start")
	} else if !strings.Contains(err.Error(), "insert process start") {
		t.Errorf("duplicate-start error = %q, want start insert context", err)
	}

	negativeStop := stop
	negativeStop.RecordID = "negative-stop-record"
	negativeStop.ProcessInstanceID = "process-2"
	negativeStop.ProcessElapsedUS = -1
	if err := store.InsertProcessStop(context.Background(), negativeStop); err == nil {
		t.Fatal("InsertProcessStop() error = nil for negative elapsed duration")
	} else if !strings.Contains(err.Error(), "insert process stop") {
		t.Errorf("negative-stop error = %q, want stop insert context", err)
	}
}
