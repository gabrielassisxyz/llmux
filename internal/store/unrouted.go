package store

import (
	"context"
	"fmt"
)

// UnroutedRequest is a locally rejected request that never reached account selection.
type UnroutedRequest struct {
	RecordID         string
	LogicalRequestID string
	StartedAtUS      int64
	FinishedAtUS     int64
	SessionKey       *string
	DownstreamStatus int
	LocalErrorCode   string
}

// InsertUnroutedRequest appends one locally rejected request in its own transaction.
func (store *Store) InsertUnroutedRequest(forceShutdown context.Context, request UnroutedRequest) error {
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()

	tx, err := store.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unrouted request transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, request.RecordID, request.LogicalRequestID, request.StartedAtUS, request.FinishedAtUS,
		request.SessionKey, request.DownstreamStatus, request.LocalErrorCode); err != nil {
		return fmt.Errorf("insert unrouted request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unrouted request: %w", err)
	}
	return nil
}
