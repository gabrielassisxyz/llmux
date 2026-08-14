package resource

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
)

// ErrOverloaded is returned when the resource gate denies admission due to
// handler slots or memory exhaustion.
var ErrOverloaded = errors.New("proxy_overloaded")

type contextKey string

const requestResourcesKey contextKey = "request_resources"

// Gate bounds process-wide resources across all concurrent requests.
// It owns the counting semaphore for concurrent admitted chat handlers
// and the weighted budget over request-owned memory.
type Gate struct {
	handlerSemaphore chan struct{}
	memoryRemaining  atomic.Int64
}

// NewGate initializes the resource gate with policy constants.
func NewGate() *Gate {
	g := &Gate{
		handlerSemaphore: make(chan struct{}, policy.ConcurrentAdmittedChatRequests),
	}
	g.memoryRemaining.Store(int64(policy.AggregateMemoryBudgetBytes))
	return g
}

// AcquireHandlerSlot waits up to policy.GlobalRequestAdmissionWait for a
// handler slot. It returns ErrOverloaded if the wait times out.
func (g *Gate) AcquireHandlerSlot(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, policy.GlobalRequestAdmissionWait)
	defer cancel()

	select {
	case g.handlerSemaphore <- struct{}{}:
		return nil
	case <-waitCtx.Done():
		return ErrOverloaded
	}
}

// ReleaseHandlerSlot releases exactly one handler slot.
func (g *Gate) ReleaseHandlerSlot() {
	select {
	case <-g.handlerSemaphore:
	default:
		panic("resource: release of unacquired handler slot")
	}
}

// AcquireMemory tries to acquire size bytes from the global budget without waiting.
// It returns ErrOverloaded if the budget cannot satisfy the request immediately.
func (g *Gate) AcquireMemory(size int) error {
	if size < 0 {
		return errors.New("negative memory size")
	}
	if size == 0 {
		return nil
	}

	delta := int64(size)
	for {
		rem := g.memoryRemaining.Load()
		if rem < delta {
			return ErrOverloaded
		}
		if g.memoryRemaining.CompareAndSwap(rem, rem-delta) {
			return nil
		}
	}
}

// ReleaseMemory returns size bytes to the global budget.
func (g *Gate) ReleaseMemory(size int) {
	if size < 0 {
		panic("resource: negative memory release")
	}
	if size == 0 {
		return
	}

	newRem := g.memoryRemaining.Add(int64(size))
	if newRem > int64(policy.AggregateMemoryBudgetBytes) {
		panic("resource: memory release exceeds budget")
	}
}

// RequestResources tracks the global resources acquired by a single request.
type RequestResources struct {
	gate         *Gate
	hasSlot      bool
	memoryCharge int
}

// AcquireMemory charges size bytes to this request's allowance from the gate.
func (r *RequestResources) AcquireMemory(size int) error {
	if err := r.gate.AcquireMemory(size); err != nil {
		return err
	}
	r.memoryCharge += size
	return nil
}

// Release returns all held resources (handler slot and memory) back to the gate.
func (r *RequestResources) Release() {
	if r.memoryCharge > 0 {
		r.gate.ReleaseMemory(r.memoryCharge)
		r.memoryCharge = 0
	}
	if r.hasSlot {
		r.gate.ReleaseHandlerSlot()
		r.hasSlot = false
	}
}

// releaseMemory returns size bytes charged earlier via AcquireMemory back to
// the gate, leaving the rest of the request's charge intact. Used to settle
// a charge down to less than what was acquired for it, rather than release
// the request's entire charge at once.
func (r *RequestResources) releaseMemory(size int) {
	if size < 0 || size > r.memoryCharge {
		panic("resource: invalid partial memory release")
	}
	r.gate.ReleaseMemory(size)
	r.memoryCharge -= size
}

// AdmissionStore is the interface for recording requests that are rejected before routing.
type AdmissionStore interface {
	RecordUnroutedRequest(ctx context.Context, reqID string, code proxy.ErrorCode)
}

// RequireResources is an HTTP middleware that acquires a handler slot before
// delegating to the next handler, and ensures both the slot and any memory
// charged to the request are released when the handler exits.
func RequireResources(g *Gate, store AdmissionStore, clk clock.Clock, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := &RequestResources{gate: g}
		defer res.Release()

		if err := g.AcquireHandlerSlot(r.Context()); err != nil {
			reqID, _ := proxy.RequestID(r.Context())
			reopen := clk.WallNow().Add(1 * time.Second)
			proxy.WriteRateLimitError(w, reqID, proxy.ErrProxyOverloaded, reopen, clk.WallNow())
			if store != nil {
				// The store context must not be the client context which might be cancelled.
				store.RecordUnroutedRequest(context.Background(), reqID, proxy.ErrProxyOverloaded)
			}
			return
		}
		res.hasSlot = true

		ctx := context.WithValue(r.Context(), requestResourcesKey, res)
		r = r.WithContext(ctx)

		next(w, r)
	}
}

// ContextResources returns the RequestResources attached to the context, or nil.
func ContextResources(ctx context.Context) *RequestResources {
	res, _ := ctx.Value(requestResourcesKey).(*RequestResources)
	return res
}
