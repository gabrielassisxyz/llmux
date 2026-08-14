package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func openPhaseBatchStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func insertPhaseBatchAdmission(t *testing.T, s *store.Store, attemptID, requestID string, attemptNo int, account string) {
	t.Helper()
	if err := s.InsertDispatchAdmission(context.Background(), store.DispatchAdmission{
		AttemptID:        attemptID,
		LogicalRequestID: requestID,
		AttemptNo:        attemptNo,
		AccountLabel:     account,
		RequestedAlias:   "alias",
		UpstreamModel:    "upstream",
		ReservedAtUS:     100,
		LimiterRPMUsed:   0,
		LimiterInFlight:  0,
	}); err != nil {
		t.Fatalf("insert dispatch admission: %v", err)
	}
}

func TestInsertPhaseBatch_AtomicCommit(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()
	insertPhaseBatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	batch := store.PhaseBatch{
		Skips: []store.PhaseSkip{
			{
				RecordID:         "rec-skip-k2",
				LogicalRequestID: "request-1",
				SequenceNo:       1,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k2",
				EventAtUS:        200,
				FinishedAtUS:     250,
				LogicalElapsedUS: 250,
				SkipReason:       store.SkipReasonRPMLimit,
				ObservationCount: 2,
			},
			{
				RecordID:         "rec-skip-k3",
				LogicalRequestID: "request-1",
				SequenceNo:       2,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k3",
				EventAtUS:        300,
				FinishedAtUS:     350,
				LogicalElapsedUS: 350,
				SkipReason:       store.SkipReasonInFlightLimit,
				ObservationCount: 1,
			},
		},
		Terminal: store.PhaseDispatch{
			RecordID:           "rec-dispatch",
			LogicalRequestID:   "request-1",
			AttemptID:          "attempt-1",
			SequenceNo:         3,
			SelectionNo:        1,
			RequestedAlias:     "alias",
			BaseAlias:          "base",
			UpstreamModel:      "upstream",
			AccountLabel:       "k1",
			AttemptNo:          1,
			EventAtUS:          400,
			FinishedAtUS:       500,
			SelectionWaitUS:    50,
			AttemptDurationUS:  phaseBatchInt64(900),
			LogicalElapsedUS:   500,
			Outcome:            store.OutcomeSucceeded,
			UpstreamStatusCode: phaseBatchInt(200),
			RetryDisposition:   store.RetryDispositionFinal,
			ResponseCommitted:  true,
			RequestStreaming:   phaseBatchBool(false),
			PromptTokens:       phaseBatchInt64(10),
			CompletionTokens:   phaseBatchInt64(20),
			TotalTokens:        phaseBatchInt64(30),
			UsageObservation:   store.UsageObservationComplete,
			DroppedHeaderCount: phaseBatchInt64(0),
		},
	}

	if err := s.InsertPhaseBatch(ctx, batch); err != nil {
		t.Fatalf("InsertPhaseBatch() error = %v", err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count attempt_log rows: %v", err)
	}
	if count != 3 {
		t.Fatalf("attempt_log count = %d, want 3", count)
	}

	var dispatchCount int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE record_kind = 'dispatch'`).Scan(&dispatchCount); err != nil {
		t.Fatalf("count dispatch rows: %v", err)
	}
	if dispatchCount != 1 {
		t.Fatalf("dispatch count = %d, want 1", dispatchCount)
	}
}

func TestInsertPhaseBatch_RollbackOnBadRow(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()
	insertPhaseBatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	batch := store.PhaseBatch{
		Skips: []store.PhaseSkip{
			{
				RecordID:         "rec-skip",
				LogicalRequestID: "request-1",
				SequenceNo:       1,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k2",
				EventAtUS:        200,
				FinishedAtUS:     250,
				LogicalElapsedUS: 250,
				SkipReason:       store.SkipReasonRPMLimit,
				ObservationCount: 1,
			},
		},
		Terminal: store.PhaseDispatch{
			RecordID:          "rec-dispatch",
			LogicalRequestID:  "request-1",
			AttemptID:         "attempt-1",
			SequenceNo:        2,
			SelectionNo:       1,
			RequestedAlias:    "alias",
			BaseAlias:         "base",
			UpstreamModel:     "upstream",
			AccountLabel:      "", // invalid: dispatch requires an account label
			AttemptNo:         1,
			EventAtUS:         400,
			FinishedAtUS:      500,
			SelectionWaitUS:   50,
			LogicalElapsedUS:  500,
			Outcome:           store.OutcomeSucceeded,
			RetryDisposition:  store.RetryDispositionFinal,
			ResponseCommitted: true,
			UsageObservation:  store.UsageObservationComplete,
		},
	}

	if err := s.InsertPhaseBatch(ctx, batch); err == nil {
		t.Fatal("InsertPhaseBatch() expected error for invalid dispatch, got nil")
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count attempt_log rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("attempt_log count = %d, want 0 after rollback", count)
	}
}

func TestInsertPhaseBatch_CapacityFailure(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()

	retryAfter := int64(10)
	batch := store.PhaseBatch{
		Skips: []store.PhaseSkip{
			{
				RecordID:         "rec-skip",
				LogicalRequestID: "request-1",
				SequenceNo:       1,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k1",
				EventAtUS:        200,
				FinishedAtUS:     250,
				LogicalElapsedUS: 250,
				SkipReason:       store.SkipReasonRateGated,
				ObservationCount: 1,
			},
		},
		Terminal: store.PhaseFailure{
			RecordID:         "rec-failure",
			LogicalRequestID: "request-1",
			SequenceNo:       2,
			SelectionNo:      1,
			RequestedAlias:   "alias",
			BaseAlias:        "base",
			UpstreamModel:    "upstream",
			EventAtUS:        300,
			FinishedAtUS:     350,
			SelectionWaitUS:  50,
			LogicalElapsedUS: 350,
			Outcome:          store.OutcomeCapacityTimeout,
			RetryDisposition: store.RetryDispositionFinal,
			RetryAfterS:      &retryAfter,
		},
	}

	if err := s.InsertPhaseBatch(ctx, batch); err != nil {
		t.Fatalf("InsertPhaseBatch() error = %v", err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count attempt_log rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("attempt_log count = %d, want 2", count)
	}

	var retryAfterRead *int64
	if err := s.Writer.QueryRowContext(ctx, `SELECT retry_after_s FROM attempt_log WHERE record_id = 'rec-failure'`).Scan(&retryAfterRead); err != nil {
		t.Fatalf("read retry_after_s: %v", err)
	}
	if retryAfterRead == nil || *retryAfterRead != 10 {
		t.Fatalf("retry_after_s = %v, want 10", retryAfterRead)
	}
}

func TestInsertPhaseBatch_ClientCanceledTerminal(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()

	batch := store.PhaseBatch{
		Terminal: store.PhaseFailure{
			RecordID:         "rec-canceled",
			LogicalRequestID: "request-1",
			SequenceNo:       1,
			SelectionNo:      1,
			RequestedAlias:   "alias",
			BaseAlias:        "base",
			UpstreamModel:    "upstream",
			EventAtUS:        100,
			FinishedAtUS:     150,
			SelectionWaitUS:  50,
			LogicalElapsedUS: 150,
			Outcome:          store.OutcomeClientCanceled,
			RetryDisposition: store.RetryDispositionFinal,
		},
	}

	if err := s.InsertPhaseBatch(ctx, batch); err != nil {
		t.Fatalf("InsertPhaseBatch() error = %v", err)
	}

	var outcome string
	if err := s.Writer.QueryRowContext(ctx, `SELECT outcome FROM attempt_log WHERE record_id = 'rec-canceled'`).Scan(&outcome); err != nil {
		t.Fatalf("read outcome: %v", err)
	}
	if outcome != string(store.OutcomeClientCanceled) {
		t.Fatalf("outcome = %q, want %q", outcome, store.OutcomeClientCanceled)
	}
}

func TestInsertPhaseBatch_DeduplicatesSkips(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()
	insertPhaseBatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	batch := store.PhaseBatch{
		Skips: []store.PhaseSkip{
			{
				RecordID:         "rec-skip-a",
				LogicalRequestID: "request-1",
				SequenceNo:       1,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k2",
				EventAtUS:        200,
				FinishedAtUS:     250,
				LogicalElapsedUS: 250,
				SkipReason:       store.SkipReasonRPMLimit,
				ObservationCount: 2,
			},
			{
				RecordID:         "rec-skip-b",
				LogicalRequestID: "request-1",
				SequenceNo:       2,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				AccountLabel:     "k2",
				EventAtUS:        300,
				FinishedAtUS:     350,
				LogicalElapsedUS: 350,
				SkipReason:       store.SkipReasonRPMLimit,
				ObservationCount: 3,
			},
		},
		Terminal: store.PhaseFailure{
			RecordID:         "rec-failure",
			LogicalRequestID: "request-1",
			SequenceNo:       3,
			SelectionNo:      1,
			RequestedAlias:   "alias",
			BaseAlias:        "base",
			UpstreamModel:    "upstream",
			EventAtUS:        400,
			FinishedAtUS:     450,
			SelectionWaitUS:  50,
			LogicalElapsedUS: 450,
			Outcome:          store.OutcomeNoAccountAvailable,
			RetryDisposition: store.RetryDispositionFinal,
		},
	}

	if err := s.InsertPhaseBatch(ctx, batch); err != nil {
		t.Fatalf("InsertPhaseBatch() error = %v", err)
	}

	var skipCount int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE record_kind = 'selection_skip' AND logical_request_id = 'request-1'`).Scan(&skipCount); err != nil {
		t.Fatalf("count skip rows: %v", err)
	}
	if skipCount != 1 {
		t.Fatalf("selection_skip count = %d, want 1", skipCount)
	}

	var total int
	if err := s.Writer.QueryRowContext(ctx, `SELECT skip_observation_count FROM attempt_log WHERE record_kind = 'selection_skip'`).Scan(&total); err != nil {
		t.Fatalf("read aggregated skip count: %v", err)
	}
	if total != 5 {
		t.Fatalf("aggregated skip observation count = %d, want 5", total)
	}
}

func TestInsertPhaseBatch_ConcurrentSerialization(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()

	const n = 10
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			requestID := fmt.Sprintf("concurrent-%d", i)
			attemptID := fmt.Sprintf("attempt-%d", i)
			insertPhaseBatchAdmission(t, s, attemptID, requestID, 1, "k1")

			batch := store.PhaseBatch{
				Skips: []store.PhaseSkip{
					{
						RecordID:         fmt.Sprintf("rec-skip-%d", i),
						LogicalRequestID: requestID,
						SequenceNo:       1,
						SelectionNo:      1,
						RequestedAlias:   "alias",
						BaseAlias:        "base",
						UpstreamModel:    "upstream",
						AccountLabel:     "k2",
						EventAtUS:        100,
						FinishedAtUS:     150,
						LogicalElapsedUS: 150,
						SkipReason:       store.SkipReasonDisabled,
						ObservationCount: 1,
					},
				},
				Terminal: store.PhaseDispatch{
					RecordID:          fmt.Sprintf("rec-dispatch-%d", i),
					LogicalRequestID:  requestID,
					AttemptID:         attemptID,
					SequenceNo:        2,
					SelectionNo:       1,
					RequestedAlias:    "alias",
					BaseAlias:         "base",
					UpstreamModel:     "upstream",
					AccountLabel:      "k1",
					AttemptNo:         1,
					EventAtUS:         200,
					FinishedAtUS:      300,
					SelectionWaitUS:   50,
					LogicalElapsedUS:  300,
					Outcome:           store.OutcomeSucceeded,
					RetryDisposition:  store.RetryDispositionFinal,
					ResponseCommitted: true,
					UsageObservation:  store.UsageObservationComplete,
				},
			}
			if err := s.InsertPhaseBatch(ctx, batch); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		t.Fatalf("concurrent InsertPhaseBatch error = %v", firstErr)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log`).Scan(&count); err != nil {
		t.Fatalf("count attempt_log rows: %v", err)
	}
	if count != n*2 {
		t.Fatalf("attempt_log count = %d, want %d", count, n*2)
	}
}

func phaseBatchInt64(v int64) *int64 {
	return &v
}

func phaseBatchInt(v int) *int {
	return &v
}

func phaseBatchBool(v bool) *bool {
	return &v
}
