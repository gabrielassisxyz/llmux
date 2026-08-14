package route

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// newReservationTestCoordinator returns a coordinator whose fake clock has
// already advanced past the post-start dispatch blackout, so tests about the
// two-phase reservation itself are not incidentally testing blackout
// behavior.
func newReservationTestCoordinator() (*Coordinator, *testsupport.FakeClock) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	c := NewCoordinator(testKeys(), fake)
	fake.AdvanceMonotonic(policy.PostStartDispatchBlackout)
	return c, fake
}

func TestReserveReturnsLeaseAndIncrementsCounters(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("outcome = %v, want Reserved", outcome)
	}
	if lease == nil {
		t.Fatal("Reserve returned nil lease with Reserved outcome")
	}
	if lease.Account() != catalog.AccountK1 {
		t.Errorf("lease.Account() = %v, want k1", lease.Account())
	}

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.inFlight != 1 {
		t.Errorf("inFlight = %d, want 1", state.inFlight)
	}
	if state.pendingReservations != 1 {
		t.Errorf("pendingReservations = %d, want 1", state.pendingReservations)
	}
	c.mu.Unlock()
}

func TestReserveSkipsDuringBlackout(t *testing.T) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	c := NewCoordinator(testKeys(), fake)

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedBlackout {
		t.Fatalf("outcome = %v, want SkippedBlackout", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease during blackout")
	}
}

func TestReserveSkipsDisabledAccount(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].health = HealthDisabled
	c.mu.Unlock()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedDisabled {
		t.Fatalf("outcome = %v, want SkippedDisabled", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease for a disabled account")
	}
}

func TestReserveSkipsGatedAccount(t *testing.T) {
	c, fake := newReservationTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + time.Minute
	c.mu.Unlock()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedGate {
		t.Fatalf("outcome = %v, want SkippedGate", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease for a gated account")
	}
}

func TestReserveSkipsInFlightSaturatedAccount(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedInFlightSaturated {
		t.Fatalf("outcome = %v, want SkippedInFlightSaturated", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease for an in-flight-saturated account")
	}
}

func TestReserveSkipsRateSaturatedAccount(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedRateSaturated {
		t.Fatalf("outcome = %v, want SkippedRateSaturated", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease for a rate-saturated account")
	}
}

func TestReserveLazilyExpiresGateBeforeChecking(t *testing.T) {
	c, fake := newReservationTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() - time.Nanosecond
	c.accounts[accountIndex(catalog.AccountK1)].health = HealthCoolingDown
	c.accounts[accountIndex(catalog.AccountK1)].recent429s = []time.Duration{1, 2, 3}
	c.mu.Unlock()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("outcome = %v, want Reserved", outcome)
	}
	if lease == nil {
		t.Fatal("Reserve returned nil lease after gate expiry")
	}

	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Errorf("health = %v, want HealthEnabled after gate expiry", c.Health(catalog.AccountK1))
	}
}

// TestPendingReservationHoldsSixtiethSlotWhileAdmissionCommitRuns proves the
// central two-phase property: a caller holding the final slot as a pending
// reservation blocks every concurrent reservation attempt for the whole time
// the admission commit is in flight, even though no timestamp has been
// recorded yet.
func TestPendingReservationHoldsSixtiethSlotWhileAdmissionCommitRuns(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	// Fill 59 finalized slots, leaving exactly one slot.
	for i := 0; i < policy.DispatchesPerWindowPerAccount-1; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	// Reserve the 60th slot but do not finalize it, simulating an
	// admission commit still in progress.
	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("60th reserve outcome = %v, want Reserved", outcome)
	}

	// A concurrent reservation must be rejected while the pending lease
	// is still open.
	_, outcome = c.Reserve(catalog.AccountK1)
	if outcome != SkippedRateSaturated {
		t.Fatalf("concurrent reserve while pending = %v, want SkippedRateSaturated", outcome)
	}

	// Finalize the first lease; the window is now truly full.
	lease.Finalize()

	_, outcome = c.Reserve(catalog.AccountK1)
	if outcome != SkippedRateSaturated {
		t.Fatalf("reserve after finalize = %v, want SkippedRateSaturated", outcome)
	}
}

func TestConcurrentFinalSlotClaimsAdmitOnlyOneCaller(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	// Saturate the window, leaving only the final slot.
	for i := 0; i < policy.DispatchesPerWindowPerAccount-1; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	const attempts = 500
	var admitted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if _, outcome := c.Reserve(catalog.AccountK1); outcome == Reserved {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := admitted.Load(); got != 1 {
		t.Fatalf("admitted = %d, want exactly 1", got)
	}

	c.mu.Lock()
	pending := c.accounts[accountIndex(catalog.AccountK1)].pendingReservations
	inFlight := c.accounts[accountIndex(catalog.AccountK1)].inFlight
	c.mu.Unlock()
	if pending != 1 {
		t.Errorf("pendingReservations = %d, want 1", pending)
	}
	if inFlight != 1 {
		t.Errorf("inFlight = %d, want 1", inFlight)
	}
}

func TestFailedAdmissionCommitReleasesBothSlots(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	lease.Release()

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.inFlight != 0 {
		t.Errorf("inFlight = %d, want 0 after release", state.inFlight)
	}
	if state.pendingReservations != 0 {
		t.Errorf("pendingReservations = %d, want 0 after release", state.pendingReservations)
	}
	c.mu.Unlock()

	// The freed capacity must be reclaimable.
	if _, outcome := c.Reserve(catalog.AccountK1); outcome != Reserved {
		t.Fatalf("reserve after release = %v, want Reserved", outcome)
	}
}

func TestFailedAdmissionCommitNotifiesWaiters(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	select {
	case <-token:
		t.Fatal("token already fired before release")
	default:
	}

	lease.Release()

	select {
	case <-token:
	default:
		t.Fatal("token did not fire after release")
	}
}

func TestFinalizeIsReleaseOnce(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	lease.Finalize()
	lease.Finalize()

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.pendingReservations != 0 {
		t.Errorf("pendingReservations = %d, want 0 after finalize", state.pendingReservations)
	}
	if len(state.dispatchTimestamps) != 1 {
		t.Errorf("dispatchTimestamps = %d, want 1", len(state.dispatchTimestamps))
	}
	if state.inFlight != 1 {
		t.Errorf("inFlight = %d, want 1 (still held)", state.inFlight)
	}
	c.mu.Unlock()
}

func TestReleaseIsReleaseOnce(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	lease.Release()
	lease.Release()

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.inFlight != 0 {
		t.Errorf("inFlight = %d, want 0 after release", state.inFlight)
	}
	if state.pendingReservations != 0 {
		t.Errorf("pendingReservations = %d, want 0 after release", state.pendingReservations)
	}
	c.mu.Unlock()
}

func TestFinalizeAfterReleaseIsIgnored(t *testing.T) {
	c, _ := newReservationTestCoordinator()

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	lease.Release()
	lease.Finalize()

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if len(state.dispatchTimestamps) != 0 {
		t.Errorf("dispatchTimestamps = %d, want 0", len(state.dispatchTimestamps))
	}
	if state.inFlight != 0 {
		t.Errorf("inFlight = %d, want 0", state.inFlight)
	}
	c.mu.Unlock()
}

// fakeAdmissionWriter is the test double the dispatch skeleton uses to
// prove that an admission-writer error prevents the upstream call. It is
// duplicated here so the route package's two-phase tests can exercise the
// full reserve-commit-finalize/do-or-release flow without importing the
// store package's own test helpers.
type fakeAdmissionWriter struct {
	err error
}

func (f *fakeAdmissionWriter) InsertDispatchAdmission(ctx context.Context, admission store.DispatchAdmission) error {
	return f.err
}

// fakeUpstreamExecutor records whether it was called, so a test can assert
// that the dispatch did not proceed when admission failed.
type fakeUpstreamExecutor struct {
	called bool
}

func (f *fakeUpstreamExecutor) Do(ctx context.Context) error {
	f.called = true
	return nil
}

// dispatchIfAdmitted is the minimal two-phase dispatch skeleton. A real
// dispatcher lives in later phases; this helper exists only to prove that a
// failed admission commit releases the lease and prevents the upstream
// call, while a successful one finalizes and calls Do.
func dispatchIfAdmitted(
	ctx context.Context,
	lease *PendingLease,
	writer store.AdmissionWriter,
	exec *fakeUpstreamExecutor,
	admission store.DispatchAdmission,
) error {
	if err := writer.InsertDispatchAdmission(ctx, admission); err != nil {
		lease.Release()
		return err
	}
	lease.Finalize()
	return exec.Do(ctx)
}

func TestNoDispatchWhenAdmissionCommitFails(t *testing.T) {
	c, _ := newReservationTestCoordinator()
	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	writer := &fakeAdmissionWriter{err: errors.New("store unavailable")}
	exec := &fakeUpstreamExecutor{}

	err := dispatchIfAdmitted(context.Background(), lease, writer, exec, store.DispatchAdmission{})
	if err == nil {
		t.Fatal("dispatchIfAdmitted() succeeded with a failing admission writer")
	}
	if exec.called {
		t.Fatal("upstream executor was called even though admission failed")
	}

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.inFlight != 0 {
		t.Errorf("inFlight = %d, want 0 after failed admission", state.inFlight)
	}
	if state.pendingReservations != 0 {
		t.Errorf("pendingReservations = %d, want 0 after failed admission", state.pendingReservations)
	}
	c.mu.Unlock()
}

func TestDispatchOccursWhenAdmissionCommitSucceeds(t *testing.T) {
	c, _ := newReservationTestCoordinator()
	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != Reserved {
		t.Fatalf("reserve outcome = %v, want Reserved", outcome)
	}

	writer := &fakeAdmissionWriter{}
	exec := &fakeUpstreamExecutor{}

	err := dispatchIfAdmitted(context.Background(), lease, writer, exec, store.DispatchAdmission{})
	if err != nil {
		t.Fatalf("dispatchIfAdmitted() error = %v", err)
	}
	if !exec.called {
		t.Fatal("upstream executor was not called after successful admission")
	}

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	if state.inFlight != 1 {
		t.Errorf("inFlight = %d, want 1 (still held during dispatch)", state.inFlight)
	}
	if state.pendingReservations != 0 {
		t.Errorf("pendingReservations = %d, want 0 after finalize", state.pendingReservations)
	}
	if len(state.dispatchTimestamps) != 1 {
		t.Errorf("dispatchTimestamps = %d, want 1", len(state.dispatchTimestamps))
	}
	c.mu.Unlock()
}
