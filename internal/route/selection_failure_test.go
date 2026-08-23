package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/errcode"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/store"
)

// wallAt is the wall-clock instant newRateTestCoordinator starts at.
func wallAt(second int) time.Time {
	return time.Date(2026, time.August, 13, 12, 0, second, 0, time.UTC)
}

func TestEarliestReopenGate(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + 30*time.Second
	c.mu.Unlock()

	reopen, ok := c.EarliestReopen()
	if !ok {
		t.Fatal("EarliestReopen() = false, want true")
	}
	if want := wallAt(30); !reopen.Equal(want) {
		t.Errorf("EarliestReopen() = %v, want %v", reopen, want)
	}
}

func TestEarliestReopenRateWindow(t *testing.T) {
	c, _ := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	reopen, ok := c.EarliestReopen()
	if !ok {
		t.Fatal("EarliestReopen() = false, want true")
	}
	// The oldest dispatch sits at the blackout boundary (60s); it falls out
	// of the window one full window later, which is 60s from now.
	if want := wallAt(60); !reopen.Equal(want) {
		t.Errorf("EarliestReopen() = %v, want %v", reopen, want)
	}
}

func TestEarliestReopenBlackout(t *testing.T) {
	c := testCoordinator()

	reopen, ok := c.EarliestReopen()
	if !ok {
		t.Fatal("EarliestReopen() = false, want true")
	}
	if want := wallAt(60); !reopen.Equal(want) {
		t.Errorf("EarliestReopen() = %v, want %v", reopen, want)
	}
}

func TestEarliestReopenTakesEarliestAcrossAccounts(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + 30*time.Second
	c.accounts[accountIndex(catalog.AccountK2)].rateGateDeadline = fake.MonotonicNow() + 10*time.Second
	c.mu.Unlock()

	reopen, ok := c.EarliestReopen()
	if !ok {
		t.Fatal("EarliestReopen() = false, want true")
	}
	if want := wallAt(10); !reopen.Equal(want) {
		t.Errorf("EarliestReopen() = %v, want %v (K2's earlier gate)", reopen, want)
	}
}

func TestEarliestReopenInFlightOnly(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].inFlight = policy.InFlightAttemptsPerAccount
	}
	c.mu.Unlock()

	if _, ok := c.EarliestReopen(); ok {
		t.Fatal("EarliestReopen() = true, want false for in-flight-only saturation")
	}
}

func TestEarliestReopenDisabledOnly(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].health = HealthDisabled
	}
	c.mu.Unlock()

	if _, ok := c.EarliestReopen(); ok {
		t.Fatal("EarliestReopen() = true, want false for disabled-only")
	}
}

func TestRetryAfterFromEarliestReopen(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + 30*time.Second
	c.mu.Unlock()

	reopen, ok := c.EarliestReopen()
	if !ok {
		t.Fatal("EarliestReopen() = false, want true")
	}
	if got := errcode.CalculateRetryAfter(reopen, fake.WallNow()); got != 30 {
		t.Errorf("Retry-After = %d, want 30", got)
	}
}

func TestRetryAfterInFlightOnlyFallsBackToOne(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].inFlight = policy.InFlightAttemptsPerAccount
	}
	c.mu.Unlock()

	reopen, ok := c.EarliestReopen()
	if ok {
		t.Fatal("EarliestReopen() = true, want false")
	}
	if got := errcode.CalculateRetryAfter(reopen, fake.WallNow()); got != 1 {
		t.Errorf("Retry-After = %d, want 1 for in-flight-only", got)
	}
}

func TestSkipReasonFor(t *testing.T) {
	cases := []struct {
		outcome ReservationOutcome
		want    store.SkipReason
	}{
		{SkippedBlackout, store.SkipReasonStartBlackout},
		{SkippedDisabled, store.SkipReasonDisabled},
		{SkippedGate, store.SkipReasonRateGated},
		{SkippedInFlightSaturated, store.SkipReasonInFlightLimit},
		{SkippedRateSaturated, store.SkipReasonRPMLimit},
	}
	for _, tc := range cases {
		if got := SkipReasonFor(tc.outcome); got != tc.want {
			t.Errorf("SkipReasonFor(%v) = %q, want %q", tc.outcome, got, tc.want)
		}
	}
}

func TestClassifySelectionFailureCapacityTimeout(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + 30*time.Second
	c.mu.Unlock()

	failure := c.ClassifySelectionFailure(SelectionResult{Outcome: SelectionCapacityTimeout}, nil)
	if failure.Code != errcode.ErrAccountCapacityTimeout {
		t.Errorf("Code = %q, want %q", failure.Code, errcode.ErrAccountCapacityTimeout)
	}
	if failure.Outcome != store.OutcomeCapacityTimeout {
		t.Errorf("Outcome = %q, want %q", failure.Outcome, store.OutcomeCapacityTimeout)
	}
	if failure.RetryAfter.IsZero() {
		t.Error("RetryAfter is zero, want the gate reopening")
	}
}

func TestClassifySelectionFailureAllDisabled(t *testing.T) {
	c, _ := newRateTestCoordinator()

	failure := c.ClassifySelectionFailure(SelectionResult{Outcome: SelectionAllDisabled}, nil)
	if failure.Code != errcode.ErrAccountUnavailable {
		t.Errorf("Code = %q, want %q", failure.Code, errcode.ErrAccountUnavailable)
	}
	if failure.Outcome != store.OutcomeNoAccountAvailable {
		t.Errorf("Outcome = %q, want %q", failure.Outcome, store.OutcomeNoAccountAvailable)
	}
	if !failure.RetryAfter.IsZero() {
		t.Error("RetryAfter should be zero for a disabled-only failure")
	}
}

func TestClassifySelectionFailureDeadline(t *testing.T) {
	c, _ := newRateTestCoordinator()

	failure := c.ClassifySelectionFailure(SelectionResult{Outcome: SelectionCanceled}, context.DeadlineExceeded)
	if failure.Code != errcode.ErrDeadlineExceeded {
		t.Errorf("Code = %q, want %q", failure.Code, errcode.ErrDeadlineExceeded)
	}
	if failure.Outcome != store.OutcomeDeadlineExceeded {
		t.Errorf("Outcome = %q, want %q", failure.Outcome, store.OutcomeDeadlineExceeded)
	}
}

func TestClassifySelectionFailureCanceled(t *testing.T) {
	c, _ := newRateTestCoordinator()

	failure := c.ClassifySelectionFailure(SelectionResult{Outcome: SelectionCanceled}, context.Canceled)
	if failure.Code != "" {
		t.Errorf("Code = %q, want empty for client cancellation", failure.Code)
	}
	if failure.Outcome != store.OutcomeClientCanceled {
		t.Errorf("Outcome = %q, want %q", failure.Outcome, store.OutcomeClientCanceled)
	}
}
