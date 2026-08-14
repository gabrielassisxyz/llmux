package resource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestBodyCharge_GrowsAheadOfCapacity(t *testing.T) {
	g := NewGate()
	res := &RequestResources{gate: g}
	step := policy.UnknownLengthBodyChargeStepBytes

	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		t.Fatalf("failed to acquire initial step: %v", err)
	}
	if charge.charged != step {
		t.Fatalf("expected initial charge of %d, got %d", step, charge.charged)
	}
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes-step) {
		t.Errorf("expected remaining budget %d after initial step, got %d", policy.AggregateMemoryBudgetBytes-step, remaining)
	}

	// A geometric backing-array growth to just past three steps must be
	// covered by the charge before that capacity is allocated.
	if err := charge.GrowTo(context.Background(), 3*step+1); err != nil {
		t.Fatalf("failed to grow charge: %v", err)
	}
	if charge.charged != 4*step {
		t.Errorf("expected charge to reach %d (next step past target), got %d", 4*step, charge.charged)
	}
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes-4*step) {
		t.Errorf("expected remaining budget %d, got %d", policy.AggregateMemoryBudgetBytes-4*step, remaining)
	}

	// A target already covered by the current charge must not grow further
	// or charge anything additional.
	if err := charge.GrowTo(context.Background(), 2*step); err != nil {
		t.Fatalf("unexpected error growing to an already-covered target: %v", err)
	}
	if charge.charged != 4*step {
		t.Errorf("charge must not shrink or grow on an already-covered target, got %d", charge.charged)
	}

	charge.Release()
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes) {
		t.Errorf("expected full budget restored after release, got %d", remaining)
	}
}

func TestBodyCharge_SettleReleasesDownToFinalCapacity(t *testing.T) {
	g := NewGate()
	res := &RequestResources{gate: g}
	step := policy.UnknownLengthBodyChargeStepBytes

	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		t.Fatalf("failed to acquire initial step: %v", err)
	}
	if err := charge.GrowTo(context.Background(), 4*step); err != nil {
		t.Fatalf("failed to grow charge: %v", err)
	}

	final := 3*step + 500
	if err := charge.Settle(final); err != nil {
		t.Fatalf("failed to settle charge: %v", err)
	}
	if charge.charged != final {
		t.Errorf("expected settled charge %d, got %d", final, charge.charged)
	}
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes-final) {
		t.Errorf("expected remaining budget %d after settle, got %d", policy.AggregateMemoryBudgetBytes-final, remaining)
	}

	if err := charge.Settle(final + 1); err == nil {
		t.Error("expected settling above the currently charged amount to fail")
	}
	if err := charge.Settle(-1); err == nil {
		t.Error("expected settling to a negative capacity to fail")
	}

	res.Release()
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes) {
		t.Errorf("expected full budget restored after request release, got %d", remaining)
	}
}

func TestBodyCharge_DeniedExtensionReleasesWholeCharge(t *testing.T) {
	g := NewGate()
	step := policy.UnknownLengthBodyChargeStepBytes

	// Drain the budget so only one and a half steps remain: enough for the
	// initial step, not enough for the next one.
	remainAfterInitial := step / 2
	drain := policy.AggregateMemoryBudgetBytes - step - remainAfterInitial
	if err := g.AcquireMemory(drain); err != nil {
		t.Fatalf("failed to drain budget: %v", err)
	}

	res := &RequestResources{gate: g}
	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		t.Fatalf("failed to acquire initial step: %v", err)
	}

	err = charge.GrowTo(context.Background(), 2*step)
	if !errors.Is(err, ErrOverloaded) {
		t.Fatalf("expected ErrOverloaded from a denied extension, got %v", err)
	}
	if charge.charged != 0 {
		t.Errorf("expected the whole charge released after a denied extension, got %d bytes still held", charge.charged)
	}
	if res.memoryCharge != 0 {
		t.Errorf("expected the request's memory charge to be released too, got %d", res.memoryCharge)
	}

	if err := g.AcquireMemory(step + remainAfterInitial); err != nil {
		t.Fatalf("expected the drained-back budget to be fully recoverable, got %v", err)
	}
}

func TestBodyCharge_ContextCancelledStopsGrowthAndReleases(t *testing.T) {
	g := NewGate()
	step := policy.UnknownLengthBodyChargeStepBytes

	res := &RequestResources{gate: g}
	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		t.Fatalf("failed to acquire initial step: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = charge.GrowTo(ctx, 5*step)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if charge.charged != 0 {
		t.Errorf("expected the whole charge released on a cancelled context, got %d bytes still held", charge.charged)
	}
	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("expected full budget recoverable after a cancelled growth, got %v", err)
	}
}

func TestBodyCharge_NegativeTargetRejected(t *testing.T) {
	g := NewGate()
	res := &RequestResources{gate: g}
	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		t.Fatalf("failed to acquire initial step: %v", err)
	}

	if err := charge.GrowTo(context.Background(), -1); err == nil {
		t.Error("expected a negative target to be rejected")
	}
	if charge.charged != policy.UnknownLengthBodyChargeStepBytes {
		t.Errorf("a rejected target must not change the charge, got %d", charge.charged)
	}
}

func TestBodyCharge_ConcurrentSmallUploadsCannotExhaustBudget(t *testing.T) {
	g := NewGate()
	step := policy.UnknownLengthBodyChargeStepBytes

	availableSteps := 5
	drain := policy.AggregateMemoryBudgetBytes - availableSteps*step
	if err := g.AcquireMemory(drain); err != nil {
		t.Fatalf("failed to drain budget: %v", err)
	}

	const attempts = 50
	charges := make([]*BodyCharge, attempts)
	var succeeded atomic.Int32
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		i := i
		go func() {
			defer wg.Done()
			res := &RequestResources{gate: g}
			charge, err := res.AcquireUnsizedBodyCharge()
			if err != nil {
				return
			}
			if err := charge.GrowTo(context.Background(), 2*step); err != nil {
				return
			}
			charges[i] = charge
			succeeded.Add(1)
		}()
	}
	wg.Wait()

	// Charges are held, not released, until every attempt has finished, so
	// this counts genuine point-in-time contention rather than a sequence of
	// non-overlapping acquire-then-release cycles.
	if got := succeeded.Load(); got > int32(availableSteps/2) {
		t.Errorf("expected at most %d two-step charges held at once from a %d-step budget, got %d", availableSteps/2, availableSteps, got)
	}

	for _, charge := range charges {
		if charge != nil {
			charge.Release()
		}
	}

	if err := g.AcquireMemory(availableSteps * step); err != nil {
		t.Fatalf("expected the whole available budget to be recoverable after concurrent attempts, got %v", err)
	}
}

func TestRequestResources_KnownLengthBodyChargesOnce(t *testing.T) {
	g := NewGate()
	res := &RequestResources{gate: g}

	if err := res.AcquireMemory(policy.MaxRequestBodyBytes); err != nil {
		t.Fatalf("expected a single charge for a maximum-size declared body, got %v", err)
	}
	if res.memoryCharge != policy.MaxRequestBodyBytes {
		t.Errorf("expected memoryCharge %d, got %d", policy.MaxRequestBodyBytes, res.memoryCharge)
	}
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes-policy.MaxRequestBodyBytes) {
		t.Errorf("expected remaining budget %d after one exact allocation, got %d", policy.AggregateMemoryBudgetBytes-policy.MaxRequestBodyBytes, remaining)
	}

	res.Release()
	if remaining := g.memoryRemaining.Load(); remaining != int64(policy.AggregateMemoryBudgetBytes) {
		t.Errorf("expected full budget restored after release, got %d", remaining)
	}
}
