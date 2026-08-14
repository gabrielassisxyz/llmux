package rewrite

import (
	"context"
	"fmt"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/store"
)

// StoreUnroutedWriter records routing-envelope rejections as unrouted_request rows.
type StoreUnroutedWriter struct {
	store     *store.Store
	generator idgen.Generator
	clk       clock.Clock
}

// NewStoreUnroutedWriter constructs a writer that persists rejections to store.
func NewStoreUnroutedWriter(store *store.Store, generator idgen.Generator, clk clock.Clock) *StoreUnroutedWriter {
	return &StoreUnroutedWriter{store: store, generator: generator, clk: clk}
}

// RecordUnroutedRequest appends one unrouted_request row for the rejection.
// It derives the row from the request ID in ctx and the fixed error code.
func (w *StoreUnroutedWriter) RecordUnroutedRequest(ctx context.Context, code proxy.ErrorCode) error {
	reqID, ok := proxy.RequestID(ctx)
	if !ok {
		return fmt.Errorf("no request ID in context")
	}

	recordID, err := w.generator.RecordID()
	if err != nil {
		return fmt.Errorf("generate record ID: %w", err)
	}

	now := w.clk.WallNow()
	return w.store.InsertUnroutedRequest(ctx, store.UnroutedRequest{
		RecordID:         recordID,
		LogicalRequestID: reqID,
		StartedAtUS:      now.UnixMicro(),
		FinishedAtUS:     now.UnixMicro(),
		DownstreamStatus: proxy.ErrorStatus(code),
		LocalErrorCode:   string(code),
	})
}
