package store

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

type fakeCheckpointRunner struct{ calls int }

func (runner *fakeCheckpointRunner) Checkpoint(context.Context) error { runner.calls++; return nil }

type observingCheckpointRunner struct {
	runner sqliteCheckpointRunner
	calls  int
}

func (runner *observingCheckpointRunner) Checkpoint(ctx context.Context) error {
	runner.calls++
	return runner.runner.Checkpoint(ctx)
}

type fakeWALSizeReader struct{ size int64 }

func (reader fakeWALSizeReader) Size() (int64, error) { return reader.size, nil }

func TestAfterTerminalCommitUsesIntervalAndThreshold(t *testing.T) {
	runner := &fakeCheckpointRunner{}
	store := &Store{checkpointRunner: runner, walSize: fakeWALSizeReader{}}
	for range policy.PassiveCheckpointIntervalCommits - 1 {
		if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || warning {
			t.Fatalf("early checkpoint = warning %v, error %v", warning, err)
		}
	}
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || warning || runner.calls != 1 {
		t.Fatalf("interval checkpoint = warning %v, error %v, calls %d", warning, err, runner.calls)
	}
	store.walSize = fakeWALSizeReader{size: policy.WALSizeWarningThresholdBytes}
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || warning || runner.calls != 2 {
		t.Fatalf("threshold checkpoint = warning %v, error %v, calls %d", warning, err, runner.calls)
	}
}

func TestAfterTerminalCommitReportsGrowingWAL(t *testing.T) {
	runner := &fakeCheckpointRunner{}
	store := &Store{checkpointRunner: runner, walSize: fakeWALSizeReader{size: policy.WALSizeWarningThresholdBytes}}
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || warning {
		t.Fatalf("first checkpoint = warning %v, error %v", warning, err)
	}
	store.walSize = fakeWALSizeReader{size: policy.WALSizeWarningThresholdBytes + 1}
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || !warning {
		t.Fatalf("growing WAL = warning %v, error %v", warning, err)
	}
}

func TestCheckpointOnShutdownUsesMaintenanceRunner(t *testing.T) {
	runner := &fakeCheckpointRunner{}
	store := &Store{checkpointRunner: runner}
	if err := store.CheckpointOnShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("checkpoint calls = %d, want 1", runner.calls)
	}
}

func TestAfterTerminalCommitWarnsWhenExternalReaderStarvesPassiveCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llmux.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	externalReader, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		t.Fatalf("open external reader: %v", err)
	}
	t.Cleanup(func() {
		if err := externalReader.Close(); err != nil {
			t.Errorf("close external reader: %v", err)
		}
	})
	readerTx, err := externalReader.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin external read transaction: %v", err)
	}
	t.Cleanup(func() {
		if err := readerTx.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Errorf("rollback external read transaction: %v", err)
		}
	})
	var rows int
	if err := readerTx.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM unrouted_request").Scan(&rows); err != nil {
		t.Fatalf("establish external reader snapshot: %v", err)
	}
	checkpoint := &observingCheckpointRunner{runner: sqliteCheckpointRunner{database: store.Maintenance}}
	store.checkpointRunner = checkpoint

	insert := func(recordID string) {
		t.Helper()
		if err := store.InsertUnroutedRequest(context.Background(), UnroutedRequest{
			RecordID: recordID, LogicalRequestID: recordID, DownstreamStatus: 400, LocalErrorCode: "invalid_request",
		}); err != nil {
			t.Fatalf("insert %s: %v", recordID, err)
		}
	}
	insert("first")

	store.terminalCommitCount = policy.PassiveCheckpointIntervalCommits - 1
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || warning {
		t.Fatalf("first starved checkpoint = warning %v, error %v", warning, err)
	}
	insert("second")
	store.terminalCommitCount = 2*policy.PassiveCheckpointIntervalCommits - 1
	if warning, err := store.AfterTerminalCommit(context.Background()); err != nil || !warning {
		t.Fatalf("growing WAL under external reader = warning %v, error %v", warning, err)
	}
	if checkpoint.calls != 2 {
		t.Fatalf("passive checkpoint calls = %d, want 2", checkpoint.calls)
	}

	var busy, logFrames, checkpointedFrames int
	if err := store.Maintenance.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		t.Fatalf("observe passive checkpoint starvation: %v", err)
	}
	if logFrames == 0 || checkpointedFrames >= logFrames {
		t.Fatalf("external reader did not hold checkpoint back: log frames %d, checkpointed frames %d", logFrames, checkpointedFrames)
	}
}
