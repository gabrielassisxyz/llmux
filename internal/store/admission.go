package store

import (
	"context"
	"fmt"
)

// AdmissionWriter is the narrow store surface a dispatch uses to commit its
// pre-dispatch evidence row. A real implementation writes to SQLite; a fake can
// return any error the dispatch path must handle as fail-closed.
type AdmissionWriter interface {
	InsertDispatchAdmission(ctx context.Context, admission DispatchAdmission) error
}

// DispatchAdmission is the pre-dispatch evidence row written synchronously after the
// in-memory reservation and before http.Client.Do.
type DispatchAdmission struct {
	AttemptID        string
	LogicalRequestID string
	AttemptNo        int
	AccountLabel     string
	RequestedAlias   string
	UpstreamModel    string
	ReservedAtUS     int64
	LimiterRPMUsed   int
	LimiterInFlight  int
}

// InsertDispatchAdmission writes one dispatch_admission row in a single synchronous
// transaction. It reports success or failure unambiguously: a non-nil error means no row
// was committed, so the caller must not proceed to dispatch.
func (store *Store) InsertDispatchAdmission(forceShutdown context.Context, admission DispatchAdmission) error {
	ctx, cancel := operationContext(store.clk, forceShutdown)
	defer cancel()

	tx, err := store.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dispatch admission transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, admission.AttemptID, admission.LogicalRequestID, admission.AttemptNo, admission.AccountLabel,
		admission.RequestedAlias, admission.UpstreamModel, admission.ReservedAtUS,
		admission.LimiterRPMUsed, admission.LimiterInFlight); err != nil {
		return fmt.Errorf("insert dispatch admission: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dispatch admission: %w", err)
	}

	return nil
}
