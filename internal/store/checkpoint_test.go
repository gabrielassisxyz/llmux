package store

import (
	"context"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

type fakeCheckpointRunner struct{ calls int }

func (runner *fakeCheckpointRunner) Checkpoint(context.Context) error { runner.calls++; return nil }

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
