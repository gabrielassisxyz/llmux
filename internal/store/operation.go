package store

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// OperationContext bounds store work from the application's force-shutdown context.
func OperationContext(forceShutdown context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(forceShutdown, policy.StoreOperationCeiling)
}
