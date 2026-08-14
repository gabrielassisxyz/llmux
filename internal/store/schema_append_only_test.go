package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

// TestAppendOnlyTriggersRejectUpdateAndDelete proves that UPDATE and DELETE
// against each of the four durable tables fail at the database level.
//
// The triggers live in migration 0007_append_only_triggers.sql. This test
// inserts a minimal valid row into each table, attempts one UPDATE and one
// DELETE per table, and asserts every attempt is rejected and leaves the
// original row untouched.
func TestAppendOnlyTriggersRejectUpdateAndDelete(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	if err := s.InsertUnroutedRequest(ctx, store.UnroutedRequest{
		RecordID:         "unrouted-1",
		LogicalRequestID: "request-1",
		StartedAtUS:      1,
		FinishedAtUS:     2,
		DownstreamStatus: 400,
		LocalErrorCode:   "invalid_request",
	}); err != nil {
		t.Fatalf("insert unrouted_request: %v", err)
	}

	if err := s.InsertProcessStart(ctx, store.ProcessStartEvent{
		RecordID:          "process-1",
		ProcessInstanceID: "instance-1",
		AtUS:              1,
		Version:           "v1",
		Revision:          "rev1",
	}); err != nil {
		t.Fatalf("insert process_event: %v", err)
	}

	if err := s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID:        "attempt-1",
		LogicalRequestID: "request-1",
		AttemptNo:        1,
		AccountLabel:     "k1",
		RequestedAlias:   "alias",
		UpstreamModel:    "upstream",
		ReservedAtUS:     1,
		LimiterRPMUsed:   0,
		LimiterInFlight:  0,
	}); err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
			time_to_first_event_us, outcome, retry_disposition, response_committed, usage_observation,
			prompt_tokens, completion_tokens, total_tokens
		) VALUES (
			'attempt-log-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			1, 2, 0, 1, 1,
			NULL, 'succeeded', 'final', 1, 'complete',
			NULL, NULL, NULL
		)
	`); err != nil {
		t.Fatalf("insert attempt_log: %v", err)
	}

	cases := []struct {
		name      string
		idColumn  string
		updateSQL string
		deleteSQL string
	}{
		{
			name:      "unrouted_request",
			idColumn:  "record_id",
			updateSQL: "UPDATE unrouted_request SET downstream_status = 500 WHERE record_id = 'unrouted-1'",
			deleteSQL: "DELETE FROM unrouted_request WHERE record_id = 'unrouted-1'",
		},
		{
			name:      "process_event",
			idColumn:  "record_id",
			updateSQL: "UPDATE process_event SET version = 'v2' WHERE record_id = 'process-1'",
			deleteSQL: "DELETE FROM process_event WHERE record_id = 'process-1'",
		},
		{
			name:      "dispatch_admission",
			idColumn:  "attempt_id",
			updateSQL: "UPDATE dispatch_admission SET account_label = 'k2' WHERE attempt_id = 'attempt-1'",
			deleteSQL: "DELETE FROM dispatch_admission WHERE attempt_id = 'attempt-1'",
		},
		{
			name:      "attempt_log",
			idColumn:  "record_id",
			updateSQL: "UPDATE attempt_log SET outcome = 'upstream_http_error' WHERE record_id = 'attempt-log-1'",
			deleteSQL: "DELETE FROM attempt_log WHERE record_id = 'attempt-log-1'",
		},
	}

	for _, c := range cases {
		t.Run(c.name+"/update", func(t *testing.T) {
			_, err := s.Writer.ExecContext(ctx, c.updateSQL)
			if err == nil {
				t.Fatalf("UPDATE on %s succeeded; want rejection", c.name)
			}
			if !strings.Contains(err.Error(), "append-only") {
				t.Fatalf("UPDATE error = %v; want append-only rejection", err)
			}
		})
		t.Run(c.name+"/delete", func(t *testing.T) {
			_, err := s.Writer.ExecContext(ctx, c.deleteSQL)
			if err == nil {
				t.Fatalf("DELETE on %s succeeded; want rejection", c.name)
			}
			if !strings.Contains(err.Error(), "append-only") {
				t.Fatalf("DELETE error = %v; want append-only rejection", err)
			}
		})
	}

	// Verify every row survived the failed mutations.
	for _, c := range cases {
		t.Run(c.name+"/row_survives", func(t *testing.T) {
			var count int
			query := "SELECT COUNT(*) FROM " + c.name + " WHERE " + c.idColumn + " = ?"
			var id string
			switch c.name {
			case "unrouted_request":
				id = "unrouted-1"
			case "process_event":
				id = "process-1"
			case "dispatch_admission":
				id = "attempt-1"
			case "attempt_log":
				id = "attempt-log-1"
			}
			if err := s.Writer.QueryRowContext(ctx, query, id).Scan(&count); err != nil {
				t.Fatalf("count rows in %s: %v", c.name, err)
			}
			if count != 1 {
				t.Fatalf("%s row count = %d; want 1", c.name, count)
			}
		})
	}
}

// TestAppendOnlySurvivesRestart proves that opening the store does not truncate
// or mutate existing rows. A seeded database has exactly the same row count
// after reopening.
func TestAppendOnlySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llmux.db")

	s1, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	ctx := context.Background()

	if err := s1.InsertUnroutedRequest(ctx, store.UnroutedRequest{
		RecordID:         "unrouted-restart",
		LogicalRequestID: "request-restart",
		StartedAtUS:      1,
		FinishedAtUS:     2,
		DownstreamStatus: 400,
		LocalErrorCode:   "invalid_request",
	}); err != nil {
		t.Fatalf("insert unrouted_request: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = s2.Close() }()

	var count int
	if err := s2.Writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM unrouted_request WHERE record_id = 'unrouted-restart'").Scan(&count); err != nil {
		t.Fatalf("count rows after restart: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after restart = %d; want 1", count)
	}
}
