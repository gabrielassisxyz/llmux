package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestProcessEventSchemaConstraints(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// 1. Successful start event.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_1', 'inst_1', 'process_start',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err != nil {
		t.Errorf("expected successful process_start insert, got: %v", err)
	}

	// 2. Successful stop event for the same instance.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_1', 'inst_1', 'process_stop',
			2000, 1000, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err != nil {
		t.Errorf("expected successful process_stop insert, got: %v", err)
	}

	// 3. Reject duplicate process_start for the same instance.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_2', 'inst_1', 'process_start',
			1500, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for duplicate process_start, got nil")
	}

	// 4. Reject start event with non-null process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_3', 'inst_2', 'process_start',
			1000, 500, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_start with elapsed time, got nil")
	}

	// 5. Reject stop event with null process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_2', 'inst_3', 'process_stop',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_stop with null elapsed time, got nil")
	}

	// 6. Reject stop event with negative process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_3', 'inst_4', 'process_stop',
			1000, -1, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_stop with negative elapsed time, got nil")
	}

	// 7. Reject invalid event_kind.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_invalid_1', 'inst_5', 'process_crash',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for invalid event_kind, got nil")
	}
}

func TestProcessEventElapsedAcrossWallClockSteps(t *testing.T) {
	s := openProcessEventStore(t)
	ctx := context.Background()

	// A backward wall-time step does not make the monotonic elapsed negative.
	mustExecProcessEvent(t, s, ctx, "bwd_start", "bwd_inst", "process_start", 2000, nil)
	mustExecProcessEvent(t, s, ctx, "bwd_stop", "bwd_inst", "process_stop", 1000, int64Ptr(500))

	elapsed, ok := elapsedForInstance(t, s, ctx, "bwd_inst")
	if !ok || elapsed != 500 {
		t.Errorf("backward wall step: expected elapsed_us=500, got %d (found=%v)", elapsed, ok)
	}

	// A forward wall-time step is also independent of the monotonic elapsed.
	mustExecProcessEvent(t, s, ctx, "fwd_start", "fwd_inst", "process_start", 1000, nil)
	mustExecProcessEvent(t, s, ctx, "fwd_stop", "fwd_inst", "process_stop", 3000, int64Ptr(500))

	elapsed, ok = elapsedForInstance(t, s, ctx, "fwd_inst")
	if !ok || elapsed != 500 {
		t.Errorf("forward wall step: expected elapsed_us=500, got %d (found=%v)", elapsed, ok)
	}
}

func TestProcessEventUnmatchedStartSurvivesBackwardClock(t *testing.T) {
	s := openProcessEventStore(t)
	ctx := context.Background()

	// The first process starts and dies before it can write a stop row.
	mustExecProcessEvent(t, s, ctx, "dead_start", "dead_inst", "process_start", 2000, nil)

	// A later process starts and stops, but its wall time is earlier than the dead one.
	mustExecProcessEvent(t, s, ctx, "alive_start", "alive_inst", "process_start", 1000, nil)
	mustExecProcessEvent(t, s, ctx, "alive_stop", "alive_inst", "process_stop", 1500, int64Ptr(500))

	var unmatched string
	err := s.Writer.QueryRowContext(ctx, `
		SELECT process_instance_id FROM process_event AS start
		WHERE start.event_kind = 'process_start'
		  AND NOT EXISTS (
			SELECT 1 FROM process_event AS stop
			WHERE stop.process_instance_id = start.process_instance_id
			  AND stop.event_kind = 'process_stop'
		  )
	`).Scan(&unmatched)
	if err != nil {
		t.Fatalf("querying unmatched start rows: %v", err)
	}
	if unmatched != "dead_inst" {
		t.Errorf("expected unmatched start to be 'dead_inst', got %q", unmatched)
	}
}

func openProcessEventStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustExecProcessEvent(t *testing.T, s *store.Store, ctx context.Context, recordID, instanceID, kind string, atUS int64, elapsed *int64) {
	t.Helper()
	var elapsedArg any
	if elapsed != nil {
		elapsedArg = *elapsed
	}
	_, err := s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (?, ?, ?, ?, ?, 'v1.0.0', 'abcdef', 3)
	`, recordID, instanceID, kind, atUS, elapsedArg)
	if err != nil {
		t.Fatalf("insert %s for %s: %v", kind, instanceID, err)
	}
}

func elapsedForInstance(t *testing.T, s *store.Store, ctx context.Context, instanceID string) (int64, bool) {
	t.Helper()
	var elapsed sql.NullInt64
	err := s.Writer.QueryRowContext(ctx, `
		SELECT process_elapsed_us FROM process_event
		WHERE process_instance_id = ? AND event_kind = 'process_stop'
	`, instanceID).Scan(&elapsed)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false
		}
		t.Fatalf("query elapsed for %s: %v", instanceID, err)
	}
	return elapsed.Int64, elapsed.Valid
}

func int64Ptr(v int64) *int64 {
	return &v
}
