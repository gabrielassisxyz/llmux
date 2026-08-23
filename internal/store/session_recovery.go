package store

import (
	"context"
	"fmt"
)

// RecoveredSessionPin is the startup-recovery result for one session key:
// the account that served the request that arrived last, and the wall-clock
// instant that request finished. FinishedAtUS anchors the recovered pin's
// expiry, which is completion-based by §16.2 rather than measured from the
// restart instant.
type RecoveredSessionPin struct {
	AccountLabel string
	FinishedAtUS int64
}

// RecoverSessionPins returns, for every session key with a successful
// completion at or after sinceUS, the account served by the request that
// arrived last, together with that request's finished_at_us. Arrival is
// derived as finished_at_us - logical_elapsed_us; ties go to the later
// finished_at_us, then the greater record_id.
//
// The derived arrival is the single documented cross-clock exposure in the
// design: a monotonic duration is subtracted from a wall instant. It is an
// exposure rather than a guarantee, because a wall-clock step between two
// finishes moves the derived arrivals against each other. The cost is bounded
// to starting the next turn on the other of two accounts that both hold a
// recent prefix.
func (store *Store) RecoverSessionPins(ctx context.Context, sinceUS int64) (map[string]RecoveredSessionPin, error) {
	rows, err := store.Writer.QueryContext(ctx, `
		SELECT session_key, account_label, finished_at_us, logical_elapsed_us, record_id
		FROM attempt_log INDEXED BY idx_attempt_log_session_recovery
		WHERE outcome = 'succeeded' AND session_key IS NOT NULL AND finished_at_us >= ?
		ORDER BY session_key, finished_at_us DESC
	`, sinceUS)
	if err != nil {
		return nil, fmt.Errorf("query session recovery: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type candidate struct {
		accountLabel string
		arrival      int64
		finishedAt   int64
		recordID     string
	}
	best := make(map[string]candidate)
	for rows.Next() {
		var sessionKey, accountLabel, recordID string
		var finishedAt, logicalElapsed int64
		if err := rows.Scan(&sessionKey, &accountLabel, &finishedAt, &logicalElapsed, &recordID); err != nil {
			return nil, fmt.Errorf("scan session recovery row: %w", err)
		}
		arrival := finishedAt - logicalElapsed
		current, ok := best[sessionKey]
		if !ok ||
			arrival > current.arrival ||
			(arrival == current.arrival && finishedAt > current.finishedAt) ||
			(arrival == current.arrival && finishedAt == current.finishedAt && recordID > current.recordID) {
			best[sessionKey] = candidate{
				accountLabel: accountLabel,
				arrival:      arrival,
				finishedAt:   finishedAt,
				recordID:     recordID,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session recovery rows: %w", err)
	}

	pins := make(map[string]RecoveredSessionPin, len(best))
	for sessionKey, c := range best {
		pins[sessionKey] = RecoveredSessionPin{
			AccountLabel: c.accountLabel,
			FinishedAtUS: c.finishedAt,
		}
	}
	return pins, nil
}
