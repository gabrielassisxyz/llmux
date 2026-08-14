package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

type mockAdmissionStore struct {
	records []string
	mu      sync.Mutex
}

func (m *mockAdmissionStore) RecordUnroutedRequest(ctx context.Context, reqID string, code proxy.ErrorCode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, reqID)
}

func TestGate_HandlerCeiling(t *testing.T) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	g := NewGateWithClock(fake)

	// Acquire all available handler slots
	for i := 0; i < policy.ConcurrentAdmittedChatRequests; i++ {
		err := g.AcquireHandlerSlot(context.Background())
		if err != nil {
			t.Fatalf("failed to acquire handler slot %d: %v", i, err)
		}
	}

	// The next one should block until the fake clock crosses the admission
	// wait, then fail with ErrOverloaded. Proven with AdvanceMonotonic
	// rather than a real 1-second sleep.
	errCh := make(chan error, 1)
	go func() {
		errCh <- g.AcquireHandlerSlot(context.Background())
	}()

	select {
	case err := <-errCh:
		t.Fatalf("AcquireHandlerSlot returned %v before the admission wait elapsed", err)
	case <-time.After(50 * time.Millisecond):
	}

	fake.AdvanceMonotonic(policy.GlobalRequestAdmissionWait)

	select {
	case err := <-errCh:
		if err != ErrOverloaded {
			t.Errorf("expected ErrOverloaded, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireHandlerSlot did not return once the admission wait elapsed")
	}

	// Release one, we should be able to acquire again
	g.ReleaseHandlerSlot()
	err := g.AcquireHandlerSlot(context.Background())
	if err != nil {
		t.Errorf("expected to acquire after release, got %v", err)
	}

	// Release all to ensure no panic
	for i := 0; i < policy.ConcurrentAdmittedChatRequests; i++ {
		g.ReleaseHandlerSlot()
	}
}

func TestGate_MemoryBudget(t *testing.T) {
	g := NewGate()

	// Exhaust the entire memory budget
	err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes)
	if err != nil {
		t.Fatalf("failed to acquire all memory: %v", err)
	}

	// Acquiring 1 more byte should fail immediately
	start := time.Now()
	err = g.AcquireMemory(1)
	dur := time.Since(start)

	if err != ErrOverloaded {
		t.Errorf("expected ErrOverloaded, got %v", err)
	}
	if dur > 100*time.Millisecond {
		t.Errorf("memory acquisition should not block, took %v", dur)
	}

	g.ReleaseMemory(policy.AggregateMemoryBudgetBytes)

	// Can acquire again
	err = g.AcquireMemory(100)
	if err != nil {
		t.Errorf("expected to acquire after release, got %v", err)
	}
	g.ReleaseMemory(100)
}

func TestGate_Middleware(t *testing.T) {
	g := NewGate()
	store := &mockAdmissionStore{}

	var handlerExecuted atomic.Bool

	next := func(w http.ResponseWriter, r *http.Request) {
		res := ContextResources(r.Context())
		if res == nil {
			t.Errorf("expected RequestResources in context")
		} else {
			if !res.hasSlot {
				t.Errorf("expected hasSlot to be true")
			}
			// Test memory acquisition inside handler
			err := res.AcquireMemory(1024)
			if err != nil {
				t.Errorf("expected memory acquisition to succeed, got %v", err)
			}
			if res.memoryCharge != 1024 {
				t.Errorf("expected memoryCharge 1024, got %d", res.memoryCharge)
			}
		}
		handlerExecuted.Store(true)
		w.WriteHeader(http.StatusOK)
	}

	wrapped := RequireResources(g, store, clock.NewRealClock(), next)

	// exhaust handler slots
	for i := 0; i < policy.ConcurrentAdmittedChatRequests; i++ {
		_ = g.AcquireHandlerSlot(context.Background())
	}

	// This request should be rejected
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	wrapped(w, req)

	res := w.Result()
	if res.StatusCode != 429 {
		t.Errorf("expected 429, got %d", res.StatusCode)
	}
	if res.Header.Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After: 1, got %q", res.Header.Get("Retry-After"))
	}
	if handlerExecuted.Load() {
		t.Errorf("handler should not have been executed")
	}

	store.mu.Lock()
	if len(store.records) != 1 {
		t.Errorf("expected 1 unrouted request recorded, got %d", len(store.records))
	}
	store.mu.Unlock()

	// Release one slot
	g.ReleaseHandlerSlot()

	// This request should succeed
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	w2 := httptest.NewRecorder()
	wrapped(w2, req2)

	res2 := w2.Result()
	if res2.StatusCode != 200 {
		t.Errorf("expected 200, got %d", res2.StatusCode)
	}
	if !handlerExecuted.Load() {
		t.Errorf("handler should have been executed")
	}

	// Ensure the memory charge and slot were released correctly
	// If slot was released, we can acquire it again
	err := g.AcquireHandlerSlot(context.Background())
	if err != nil {
		t.Errorf("handler slot was not released: %v", err)
	}

	// If memory was released, we can acquire the full budget again
	err = g.AcquireMemory(policy.AggregateMemoryBudgetBytes)
	if err != nil {
		t.Errorf("memory was not fully released: %v", err)
	}
}

func TestGate_ConcurrentWaiters(t *testing.T) {
	// "thousands of small requests waiting on the gate create neither unbounded goroutines nor unbounded waiter metadata."
	// We will simulate 2000 concurrent requests when slots are full.
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	g := NewGateWithClock(fake)
	for i := 0; i < policy.ConcurrentAdmittedChatRequests; i++ {
		_ = g.AcquireHandlerSlot(context.Background())
	}

	var wg sync.WaitGroup
	waiters := 2000
	wg.Add(waiters)

	// Track how many were rejected
	var rejections atomic.Int32

	for i := 0; i < waiters; i++ {
		go func() {
			defer wg.Done()
			err := g.AcquireHandlerSlot(context.Background())
			if err == ErrOverloaded {
				rejections.Add(1)
			}
		}()
	}

	// Give the waiters time to register their timers on the fake clock
	// before advancing, since NewTimer must be called before the advance
	// can find it due.
	time.Sleep(200 * time.Millisecond)
	fake.AdvanceMonotonic(policy.GlobalRequestAdmissionWait)
	wg.Wait()

	if int(rejections.Load()) != waiters {
		t.Errorf("expected %d rejections, got %d", waiters, rejections.Load())
	}
}
