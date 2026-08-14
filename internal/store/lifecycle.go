package store

import (
	"context"
	"fmt"
)

// ProcessStartEvent records a process startup after database migration succeeds.
type ProcessStartEvent struct {
	RecordID          string
	ProcessInstanceID string
	AtUS              int64
	Version           string
	Revision          string
}

// ProcessStopEvent records a process shutdown before the store closes.
type ProcessStopEvent struct {
	RecordID          string
	ProcessInstanceID string
	AtUS              int64
	ProcessElapsedUS  int64
	Version           string
	Revision          string
}

// InsertProcessStart appends one process_start event.
func (store *Store) InsertProcessStart(forceShutdown context.Context, event ProcessStartEvent) error {
	return store.insertProcessEvent(forceShutdown, event.RecordID, event.ProcessInstanceID, "process_start", "process start", event.AtUS, nil, event.Version, event.Revision)
}

// InsertProcessStop appends one process_stop event.
func (store *Store) InsertProcessStop(forceShutdown context.Context, event ProcessStopEvent) error {
	elapsed := event.ProcessElapsedUS
	return store.insertProcessEvent(forceShutdown, event.RecordID, event.ProcessInstanceID, "process_stop", "process stop", event.AtUS, &elapsed, event.Version, event.Revision)
}

func (store *Store) insertProcessEvent(forceShutdown context.Context, recordID, processInstanceID, eventKind, operation string, atUS int64, elapsedUS *int64, version, revision string) error {
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()

	tx, err := store.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s transaction: %w", operation, err)
	}
	defer func() { _ = tx.Rollback() }()

	var schemaVersion int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return fmt.Errorf("read schema version for %s: %w", operation, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind, at_us,
			process_elapsed_us, version, revision, schema_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, recordID, processInstanceID, eventKind, atUS, elapsedUS, version, revision, schemaVersion); err != nil {
		return fmt.Errorf("insert %s: %w", operation, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}
