package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestInsertUnroutedRequestPersistsRowAndRejectsConstraintViolations(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	request := UnroutedRequest{
		RecordID:         "record-1",
		LogicalRequestID: "request-1",
		StartedAtUS:      100,
		FinishedAtUS:     200,
		DownstreamStatus: 400,
		LocalErrorCode:   "invalid_request",
	}
	if err := store.InsertUnroutedRequest(context.Background(), request); err != nil {
		t.Fatalf("InsertUnroutedRequest() error = %v", err)
	}

	var sessionKey sql.NullString
	var status int
	if err := store.Writer.QueryRowContext(context.Background(), `
		SELECT session_key, downstream_status
		FROM unrouted_request
		WHERE record_id = ?
	`, request.RecordID).Scan(&sessionKey, &status); err != nil {
		t.Fatalf("query inserted unrouted request: %v", err)
	}
	if sessionKey.Valid {
		t.Errorf("session_key valid = true, want false")
	}
	if status != request.DownstreamStatus {
		t.Errorf("downstream_status = %d, want %d", status, request.DownstreamStatus)
	}

	invalidCode := request
	invalidCode.RecordID = "record-2"
	invalidCode.LogicalRequestID = "request-2"
	invalidCode.LocalErrorCode = "not-a-proxy-error"
	if err := store.InsertUnroutedRequest(context.Background(), invalidCode); err == nil {
		t.Fatal("InsertUnroutedRequest() error = nil for an unsupported local error code")
	} else if !strings.Contains(err.Error(), "insert unrouted request") {
		t.Errorf("unsupported-code error = %q, want insert context", err)
	}

	duplicateRequest := request
	duplicateRequest.RecordID = "record-3"
	if err := store.InsertUnroutedRequest(context.Background(), duplicateRequest); err == nil {
		t.Fatal("InsertUnroutedRequest() error = nil for duplicate logical request ID")
	}
}

func TestInsertUnroutedRequestReportsEveryFailureAfterWriterCloses(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	if err := store.Writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	for index := range 3 {
		err := store.InsertUnroutedRequest(context.Background(), UnroutedRequest{
			RecordID:         fmt.Sprintf("closed-writer-record-%d", index),
			LogicalRequestID: fmt.Sprintf("closed-writer-request-%d", index),
			DownstreamStatus: 503,
			LocalErrorCode:   "proxy_overloaded",
		})
		if err == nil {
			t.Fatalf("InsertUnroutedRequest() error = nil after writer closes on call %d", index+1)
		}
	}
}
