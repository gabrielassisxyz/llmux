package store

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// OperationContext bounds store work from the application's force-shutdown
// context using the real clock. It is the production entry point for store
// paths that do not carry an injected clock; the store methods that do use
// operationContext with the store's clock instead.
func OperationContext(forceShutdown context.Context) (context.Context, context.CancelFunc) {
	return operationContext(clock.NewRealClock(), forceShutdown)
}

// operationContext bounds store work from the application's force-shutdown
// context and the store-operation ceiling. The deadline is anchored at the
// injected clock's wall instant rather than the process wall clock, so a
// test can supply a fake clock and the deadline never fires for a
// wall-clock reason. The deadline still reports context.DeadlineExceeded
// when it elapses, matching the context.WithTimeout contract the callers
// rely on.
func operationContext(clk clock.Clock, forceShutdown context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(forceShutdown, clk.WallNow().Add(policy.StoreOperationCeiling))
}
