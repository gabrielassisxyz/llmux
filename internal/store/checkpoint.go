package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

type checkpointRunner interface {
	Checkpoint(context.Context) error
}

type sqliteCheckpointRunner struct {
	database *sql.DB
}

func (runner sqliteCheckpointRunner) Checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointedFrames int
	if err := runner.database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("run passive checkpoint: %w", err)
	}
	return nil
}

type walSizeReader interface {
	Size() (int64, error)
}

type fileWALSize struct {
	path string
}

func (reader fileWALSize) Size() (int64, error) {
	info, err := os.Stat(reader.path + "-wal")
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat WAL: %w", err)
	}
	return info.Size(), nil
}

// AfterTerminalCommit attempts a passive maintenance checkpoint when the interval or WAL threshold requires it.
func (store *Store) AfterTerminalCommit(forceShutdown context.Context) (bool, error) {
	store.checkpointMu.Lock()
	defer store.checkpointMu.Unlock()

	store.terminalCommitCount++
	walBytes, err := store.walSize.Size()
	if err != nil {
		return false, err
	}
	if store.terminalCommitCount%policy.PassiveCheckpointIntervalCommits != 0 && walBytes < policy.WALSizeWarningThresholdBytes {
		return false, nil
	}
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()
	if err := store.checkpointRunner.Checkpoint(ctx); err != nil {
		return false, err
	}
	warning := store.lastCheckpointWAL > 0 && walBytes > store.lastCheckpointWAL
	store.lastCheckpointWAL = walBytes
	return warning, nil
}

// CheckpointOnShutdown attempts the final passive maintenance checkpoint after the stop row.
func (store *Store) CheckpointOnShutdown(forceShutdown context.Context) error {
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()
	return store.checkpointRunner.Checkpoint(ctx)
}
