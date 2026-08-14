package store_test

import (
	"context"
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

func TestProcessEventRejectsImpossibleLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// Seed a clean start/stop pair so the table is not empty, and establish
	// that the schema allows a normal lifecycle.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES ('rec_start', 'inst', 'process_start', 1000, NULL, 'v1', 'rev', 3)
	`)
	if err != nil {
		t.Fatalf("seed start: %v", err)
	}
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES ('rec_stop', 'inst', 'process_stop', 2000, 1000, 'v1', 'rev', 3)
	`)
	if err != nil {
		t.Fatalf("seed stop: %v", err)
	}

	cases := []struct {
		name         string
		stmt         string
		expectReject bool
	}{
		{
			name: "second stop without second start",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_stop_2', 'inst', 'process_stop', 3000, 1000, 'v1', 'rev', 3)`,
			expectReject: true,
		},
		{
			name: "stop row before any start row",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_orphan_stop', 'new_inst', 'process_stop', 1000, 1, 'v1', 'rev', 3)`,
			// The schema does not enforce a foreign-key ordering between start and stop rows.
			// A stop row without a matching start is admissible because the schema only knows
			// the per-instance uniqueness of each event kind, not the temporal sequence.
			expectReject: false,
		},
		{
			name: "negative elapsed duration",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_neg', 'neg_inst', 'process_stop', 1000, -1, 'v1', 'rev', 3)`,
			expectReject: true,
		},
		{
			name: "start row with elapsed duration",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_start_with_elapsed', 'start_elapsed_inst', 'process_start', 1000, 1, 'v1', 'rev', 3)`,
			expectReject: true,
		},
		{
			name: "duplicate start for same instance",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_dup_start', 'inst', 'process_start', 3000, NULL, 'v1', 'rev', 3)`,
			expectReject: true,
		},
		{
			name: "duplicate stop for same instance",
			stmt: `INSERT INTO process_event (
				record_id, process_instance_id, event_kind,
				at_us, process_elapsed_us, version, revision, schema_version
			) VALUES ('rec_dup_stop', 'inst', 'process_stop', 3000, 1000, 'v1', 'rev', 3)`,
			expectReject: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Writer.ExecContext(ctx, tc.stmt)
			rejected := err != nil
			if rejected != tc.expectReject {
				t.Errorf("%q: rejected=%v, want %v (err=%v)", tc.name, rejected, tc.expectReject, err)
			}
		})
	}
}
