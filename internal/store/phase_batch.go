package store

import (
	"context"
	"database/sql"
	"fmt"
)

// PhaseSkip records one aggregated account/reason skip from a selection phase.
// Multiple observations for the same account and reason are combined into a
// single row by InsertPhaseBatch.
type PhaseSkip struct {
	RecordID          string
	LogicalRequestID  string
	SequenceNo        int
	SelectionNo       int
	RequestedAlias    string
	BaseAlias         string
	UpstreamModel     string
	SessionKey        *string
	PinAccountAtStart *string
	AccountLabel      string
	EventAtUS         int64
	FinishedAtUS      int64
	LogicalElapsedUS  int64
	SkipReason        SkipReason
	ObservationCount  int
}

// PhaseDispatch records the terminal row for a selection phase that resulted
// in an upstream dispatch.
type PhaseDispatch struct {
	RecordID            string
	LogicalRequestID    string
	AttemptID           string
	SequenceNo          int
	SelectionNo         int
	RequestedAlias      string
	BaseAlias           string
	UpstreamModel       string
	SessionKey          *string
	PinAccountAtStart   *string
	AccountLabel        string
	AttemptNo           int
	IsSpill             bool
	SpillFromAccount    *string
	EventAtUS           int64
	FinishedAtUS        int64
	SelectionWaitUS     int64
	AttemptDurationUS   *int64
	LogicalElapsedUS    int64
	TimeToFirstEventUS  *int64
	Outcome             Outcome
	UpstreamStatusCode  *int
	ErrorClass          *ErrorClass
	RetryDisposition    RetryDisposition
	RetryDelayMS        *int64
	RetryAfterS         *int64
	UpstreamRetryAfterS *int64
	ResponseCommitted   bool
	RequestStreaming    *bool
	PromptTokens        *int64
	CompletionTokens    *int64
	TotalTokens         *int64
	UsageObservation    UsageObservation
	DroppedHeaderCount  *int64
}

// PhaseFailure records the terminal row for a selection phase that ended
// without dispatch.
type PhaseFailure struct {
	RecordID          string
	LogicalRequestID  string
	SequenceNo        int
	SelectionNo       int
	RequestedAlias    string
	BaseAlias         string
	UpstreamModel     string
	SessionKey        *string
	PinAccountAtStart *string
	EventAtUS         int64
	FinishedAtUS      int64
	SelectionWaitUS   int64
	LogicalElapsedUS  int64
	Outcome           Outcome
	RetryDisposition  RetryDisposition
	RetryAfterS       *int64
}

// PhaseBatch is the unit of evidence committed for one terminal routing phase.
// It contains zero or more deduplicated skip rows and exactly one terminal row.
type PhaseBatch struct {
	Skips    []PhaseSkip
	Terminal PhaseTerminal
}

// PhaseTerminal is the closed union of terminal row kinds a phase batch can carry.
type PhaseTerminal interface {
	phaseTerminal()
}

func (PhaseDispatch) phaseTerminal() {}
func (PhaseFailure) phaseTerminal()  {}

// PhaseBatchWriter is the narrow store surface a dispatch handler uses to
// commit the terminal evidence for one selection phase.
type PhaseBatchWriter interface {
	InsertPhaseBatch(ctx context.Context, batch PhaseBatch) error
}

// InsertPhaseBatch writes all rows for one terminal selection phase in a single
// SQLite transaction. The batch contains deduplicated skip rows and exactly one
// terminal dispatch or selection_failure row. A non-nil error means none of the
// rows were committed.
func (store *Store) InsertPhaseBatch(forceShutdown context.Context, batch PhaseBatch) error {
	if batch.Terminal == nil {
		return fmt.Errorf("phase batch has no terminal row")
	}

	ctx, cancel := operationContext(store.clk, forceShutdown)
	defer cancel()

	tx, err := store.Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin phase batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deduped := dedupeSkips(batch.Skips)
	for _, skip := range deduped {
		if err := insertPhaseSkip(ctx, tx, skip); err != nil {
			return fmt.Errorf("insert phase skip: %w", err)
		}
	}

	switch terminal := batch.Terminal.(type) {
	case PhaseDispatch:
		if err := insertPhaseDispatch(ctx, tx, terminal); err != nil {
			return fmt.Errorf("insert phase dispatch: %w", err)
		}
	case PhaseFailure:
		if err := insertPhaseFailure(ctx, tx, terminal); err != nil {
			return fmt.Errorf("insert phase failure: %w", err)
		}
	default:
		return fmt.Errorf("unsupported terminal type %T", terminal)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit phase batch: %w", err)
	}

	return nil
}

func dedupeSkips(skips []PhaseSkip) []PhaseSkip {
	type key struct {
		account string
		reason  string
	}

	order := make([]key, 0, len(skips))
	seen := make(map[key]*PhaseSkip, len(skips))

	for i := range skips {
		skip := skips[i]
		k := key{account: skip.AccountLabel, reason: string(skip.SkipReason)}
		if existing, ok := seen[k]; ok {
			existing.ObservationCount += skip.ObservationCount
			continue
		}
		seen[k] = &skip
		order = append(order, k)
	}

	deduped := make([]PhaseSkip, 0, len(order))
	for _, k := range order {
		deduped = append(deduped, *seen[k])
	}
	return deduped
}

func insertPhaseSkip(ctx context.Context, tx *sql.Tx, skip PhaseSkip) error {
	if skip.ObservationCount < 1 {
		return fmt.Errorf("skip %s observation count %d is not positive", skip.RecordID, skip.ObservationCount)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
			account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
			selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
			outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
			retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
			prompt_tokens, completion_tokens, total_tokens, usage_observation,
			limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
			dropped_header_count
		) VALUES (
			?, ?, NULL, ?, ?, 'selection_skip',
			?, ?, ?, ?, ?,
			?, NULL, 0, NULL, ?, ?,
			NULL, NULL, ?, NULL,
			'selection_skipped', NULL, 'local_capacity', 'not_applicable', NULL,
			NULL, NULL, 0, NULL,
			NULL, NULL, NULL, NULL,
			0, 0, ?, ?,
			NULL
		)
	`, skip.RecordID, skip.LogicalRequestID, skip.SequenceNo, skip.SelectionNo,
		skip.RequestedAlias, skip.BaseAlias, skip.UpstreamModel, skip.SessionKey, skip.PinAccountAtStart,
		skip.AccountLabel, skip.EventAtUS, skip.FinishedAtUS,
		skip.LogicalElapsedUS,
		string(skip.SkipReason), skip.ObservationCount); err != nil {
		return fmt.Errorf("insert skip %s: %w", skip.RecordID, err)
	}

	return nil
}

func insertPhaseDispatch(ctx context.Context, tx *sql.Tx, dispatch PhaseDispatch) error {
	if dispatch.UsageObservation == "" {
		return fmt.Errorf("dispatch %s usage_observation is required", dispatch.RecordID)
	}

	spillFrom := optionalSpillFrom(dispatch.IsSpill, dispatch.SpillFromAccount)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
			account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
			selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
			outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
			retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
			prompt_tokens, completion_tokens, total_tokens, usage_observation,
			limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
			dropped_header_count
		) VALUES (
			?, ?, ?, ?, ?, 'dispatch',
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?,
			NULL, NULL, NULL, NULL,
			?
		)
	`, dispatch.RecordID, dispatch.LogicalRequestID, dispatch.AttemptID, dispatch.SequenceNo, dispatch.SelectionNo,
		dispatch.RequestedAlias, dispatch.BaseAlias, dispatch.UpstreamModel, dispatch.SessionKey, dispatch.PinAccountAtStart,
		dispatch.AccountLabel, dispatch.AttemptNo, boolInt(dispatch.IsSpill), spillFrom, dispatch.EventAtUS, dispatch.FinishedAtUS,
		dispatch.SelectionWaitUS, dispatch.AttemptDurationUS, dispatch.LogicalElapsedUS, dispatch.TimeToFirstEventUS,
		string(dispatch.Outcome), dispatch.UpstreamStatusCode, optionalErrorClass(dispatch.ErrorClass), string(dispatch.RetryDisposition), dispatch.RetryDelayMS,
		dispatch.RetryAfterS, dispatch.UpstreamRetryAfterS, boolInt(dispatch.ResponseCommitted), optionalBoolInt(dispatch.RequestStreaming),
		dispatch.PromptTokens, dispatch.CompletionTokens, dispatch.TotalTokens, string(dispatch.UsageObservation),
		dispatch.DroppedHeaderCount); err != nil {
		return fmt.Errorf("insert dispatch %s: %w", dispatch.RecordID, err)
	}

	return nil
}

func insertPhaseFailure(ctx context.Context, tx *sql.Tx, failure PhaseFailure) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
			account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
			selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
			outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
			retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
			prompt_tokens, completion_tokens, total_tokens, usage_observation,
			limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
			dropped_header_count
		) VALUES (
			?, ?, NULL, ?, ?, 'selection_failure',
			?, ?, ?, ?, ?,
			NULL, NULL, 0, NULL, ?, ?,
			?, NULL, ?, NULL,
			?, NULL, NULL, ?, NULL,
			?, NULL, 0, 0,
			NULL, NULL, NULL, NULL,
			NULL, NULL, NULL, NULL,
			NULL
		)
	`, failure.RecordID, failure.LogicalRequestID, failure.SequenceNo, failure.SelectionNo,
		failure.RequestedAlias, failure.BaseAlias, failure.UpstreamModel, failure.SessionKey, failure.PinAccountAtStart,
		failure.EventAtUS, failure.FinishedAtUS,
		failure.SelectionWaitUS, failure.LogicalElapsedUS,
		string(failure.Outcome), string(failure.RetryDisposition),
		failure.RetryAfterS); err != nil {
		return fmt.Errorf("insert failure %s: %w", failure.RecordID, err)
	}

	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func optionalBoolInt(b *bool) any {
	if b == nil {
		return nil
	}
	return boolInt(*b)
}

func optionalErrorClass(e *ErrorClass) any {
	if e == nil {
		return nil
	}
	return string(*e)
}

func optionalSpillFrom(isSpill bool, account *string) any {
	if !isSpill {
		return nil
	}
	return account
}
