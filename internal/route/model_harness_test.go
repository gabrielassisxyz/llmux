package route

import (
	"context"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// modelHarness drives the real Coordinator and the reference model in
// model_test.go through the same operation and compares the two. Every
// coordinator entry point this suite exercises has exactly one method here,
// so the randomised walk in model_machine_test.go and the scripted scenario
// in model_scenario_test.go drive identical code and differ only in how
// they choose what to call next.

// modelWallStart is the fixed wall instant every run starts from. It is a
// constant rather than the real clock so a stored reproducer replays
// against the same instants it was recorded at.
var modelWallStart = time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)

var modelAccountKeys = AccountKeys{K1: "k1-key", K2: "k2-key", K3: "k3-key"}

// scriptedPermutationSource is the named deterministic permutation source
// the hermetic-test rule requires. The caller installs the order before
// each selection so the reference model can be walked through the identical
// candidate sequence; an unset or wrongly sized order falls back to the
// identity permutation rather than panicking mid-sequence.
type scriptedPermutationSource struct {
	order []int
}

func (source *scriptedPermutationSource) Perm(n int) []int {
	if len(source.order) != n {
		identity := make([]int, n)
		for i := range identity {
			identity[i] = i
		}
		return identity
	}
	return append([]int(nil), source.order...)
}

// heldLease is one reservation the driver still owns, tracked so a lease is
// finalized or released at most once. Calling either twice is a caller
// error the release-once guard absorbs, not a transition worth driving.
type heldLease struct {
	lease     *PendingLease
	index     int
	finalized bool
}

type modelHarness struct {
	clock       *testsupport.FakeClock
	permutation *scriptedPermutationSource
	coordinator *Coordinator
	model       *coordinatorModel
	coverage    *transitionCoverage

	leases []*heldLease
	// rateSlots counts pending slots taken through the rate-only entry
	// point per account, each owing either a dispatch or a release.
	rateSlots [3]int
	// ownedInFlight counts in-flight slots taken through
	// IncrementInFlight, which owes a matching decrement. Lease-owned
	// slots are not counted here; they are released with their lease.
	ownedInFlight [3]int
}

func newModelHarness(coverage *transitionCoverage) *modelHarness {
	clock := testsupport.NewFakeClock(modelWallStart)
	permutation := &scriptedPermutationSource{}
	return &modelHarness{
		clock:       clock,
		permutation: permutation,
		coordinator: NewCoordinator(modelAccountKeys, clock, permutation),
		model:       newCoordinatorModel(),
		coverage:    coverage,
	}
}

func (h *modelHarness) now() time.Duration            { return h.clock.MonotonicNow() }
func (h *modelHarness) wall() time.Time               { return h.clock.WallNow() }
func (h *modelHarness) account(i int) catalog.Account { return accountFromIndex(i) }

// snapshot copies the coordinator's own state into model shape. Reading the
// unexported fields directly is what keeps this suite from forcing an
// inspection API into production code that nothing else would use.
func (h *modelHarness) snapshot() *coordinatorModel {
	h.coordinator.mu.Lock()
	defer h.coordinator.mu.Unlock()

	snapshot := newCoordinatorModel()
	snapshot.sessionOps = h.coordinator.sessionOps
	for i := range h.coordinator.accounts {
		state := &h.coordinator.accounts[i]
		snapshot.accounts[i] = modelAccount{
			dispatchTimestamps:  append([]time.Duration(nil), state.dispatchTimestamps...),
			pendingReservations: state.pendingReservations,
			inFlight:            state.inFlight,
			health:              state.health,
			rateGateDeadline:    state.rateGateDeadline,
			recent429s:          append([]time.Duration(nil), state.recent429s...),
		}
	}
	// The conversion compiles only while the two pin records carry the same
	// fields in the same order, which is the point of writing it this way: a
	// field added to the implementation's pin becomes a compile error here
	// rather than a field the comparison silently never looks at.
	for key, pin := range h.coordinator.pins {
		snapshot.pins[key] = modelPin(pin)
	}
	return snapshot
}

func sameInstants(actual, expected []time.Duration) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

// compare is the step-by-step agreement check. It reports the first field
// that disagrees, named, because a dump of two whole structs is not a
// diagnosis.
func (h *modelHarness) compare(t modelTB) {
	t.Helper()
	actual := h.snapshot()

	for i := range actual.accounts {
		got, want := actual.accounts[i], h.model.accounts[i]
		label := h.account(i)
		if !sameInstants(got.dispatchTimestamps, want.dispatchTimestamps) {
			t.Fatalf("%s dispatch timestamps = %v, model says %v", label, got.dispatchTimestamps, want.dispatchTimestamps)
		}
		if got.pendingReservations != want.pendingReservations {
			t.Fatalf("%s pending reservations = %d, model says %d", label, got.pendingReservations, want.pendingReservations)
		}
		if got.inFlight != want.inFlight {
			t.Fatalf("%s in flight = %d, model says %d", label, got.inFlight, want.inFlight)
		}
		if got.health != want.health {
			t.Fatalf("%s health = %v, model says %v", label, got.health, want.health)
		}
		if got.rateGateDeadline != want.rateGateDeadline {
			t.Fatalf("%s gate deadline = %v, model says %v", label, got.rateGateDeadline, want.rateGateDeadline)
		}
		if !sameInstants(got.recent429s, want.recent429s) {
			t.Fatalf("%s recent 429s = %v, model says %v", label, got.recent429s, want.recent429s)
		}
	}

	if actual.sessionOps != h.model.sessionOps {
		t.Fatalf("session op counter = %d, model says %d", actual.sessionOps, h.model.sessionOps)
	}
	if len(actual.pins) != len(h.model.pins) {
		t.Fatalf("pin count = %d, model says %d", len(actual.pins), len(h.model.pins))
	}
	for key, got := range actual.pins {
		want, ok := h.model.pins[key]
		if !ok {
			t.Fatalf("pin %x exists, model has none", key[:4])
		}
		if got != want {
			t.Fatalf("pin %x = %+v, model says %+v", key[:4], got, want)
		}
	}
}

// checkInvariants asserts the properties this package's documented
// invariants claim hold at every observation point, so the suite proves
// them rather than only proving that two statements of the same rules agree
// with each other.
func (h *modelHarness) checkInvariants(t modelTB) {
	t.Helper()
	state := h.snapshot()

	for i := range state.accounts {
		account := state.accounts[i]
		label := h.account(i)
		if account.inFlight < 0 || account.inFlight > policy.InFlightAttemptsPerAccount {
			t.Fatalf("invariant 5: %s in flight = %d, want within [0, %d]",
				label, account.inFlight, policy.InFlightAttemptsPerAccount)
		}
		if account.pendingReservations < 0 {
			t.Fatalf("invariant 6: %s pending reservations = %d, want non-negative", label, account.pendingReservations)
		}
		occupied := len(account.dispatchTimestamps) + account.pendingReservations
		if occupied > policy.DispatchesPerWindowPerAccount {
			t.Fatalf("invariant 6: %s occupies %d window slots, want at most %d",
				label, occupied, policy.DispatchesPerWindowPerAccount)
		}
		switch account.health {
		case HealthEnabled, HealthCoolingDown, HealthDisabled:
		default:
			t.Fatalf("%s health = %d, want one of the three declared states", label, account.health)
		}
		if account.health == HealthCoolingDown && account.rateGateDeadline == 0 {
			t.Fatalf("invariant 16: %s is cooling down with no gate deadline", label)
		}
	}

	for key, pin := range state.pins {
		switch pin.state {
		case PinProvisional:
			if pin.holders < 1 {
				t.Fatalf("invariant 30: provisional pin %x is live with %d holders", key[:4], pin.holders)
			}
		case PinConfirmed:
			if pin.holders != 0 || pin.generation != 0 {
				t.Fatalf("confirmed pin %x carries holders=%d generation=%d, want both zero",
					key[:4], pin.holders, pin.generation)
			}
		}
	}
}

// step is the check both drivers run after every operation.
func (h *modelHarness) step(t modelTB) {
	t.Helper()
	h.compare(t)
	h.checkInvariants(t)
}

// endedContext is the context every selection in this suite is handed. A
// pass that cannot reserve waits, and in a single-goroutine sequence nothing
// will ever notify it, so the wait has to end by itself: an already-ended
// context makes the failed pass return at once and keeps each operation to
// exactly one selection pass.
func endedContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func (h *modelHarness) reserve(t modelTB, index int) ReservationOutcome {
	t.Helper()
	now := h.now()

	lease, outcome := h.coordinator.Reserve(h.account(index))
	expected := h.model.reserve(index, now)
	if outcome != expected {
		t.Fatalf("Reserve(%s) outcome = %v, model says %v", h.account(index), outcome, expected)
	}
	if (lease != nil) != (outcome == Reserved) {
		t.Fatalf("Reserve(%s) returned lease=%v with outcome %v", h.account(index), lease != nil, outcome)
	}
	h.coverage.note("reserve:" + reservationOutcomeName(outcome))
	if lease != nil {
		h.leases = append(h.leases, &heldLease{lease: lease, index: index})
	}
	h.step(t)
	return outcome
}

// dispatch drives one rate-only reservation and, when it is granted, the
// dispatch that consumes it. It is the pair that fills the rolling window
// without touching in-flight, which is the only way to reach the per-window
// ceiling in a sequence short enough to read.
func (h *modelHarness) dispatch(t modelTB, index int) bool {
	t.Helper()

	granted := h.coordinator.ReserveRateSlot(h.account(index))
	if expected := h.model.reserveRateSlot(index, h.now()); granted != expected {
		t.Fatalf("ReserveRateSlot(%s) = %v, model says %v", h.account(index), granted, expected)
	}
	if !granted {
		h.coverage.note("rateSlot:refused")
		h.step(t)
		return false
	}
	h.coverage.note("rateSlot:granted")

	h.coordinator.FinalizeDispatch(h.account(index))
	h.model.finalizeDispatch(index, h.now())
	h.coverage.note("rateSlot:dispatched")
	h.step(t)
	return true
}

func (h *modelHarness) reserveRateSlot(t modelTB, index int) bool {
	t.Helper()

	granted := h.coordinator.ReserveRateSlot(h.account(index))
	if expected := h.model.reserveRateSlot(index, h.now()); granted != expected {
		t.Fatalf("ReserveRateSlot(%s) = %v, model says %v", h.account(index), granted, expected)
	}
	if granted {
		h.rateSlots[index]++
		h.coverage.note("rateSlot:granted")
	} else {
		h.coverage.note("rateSlot:refused")
	}
	h.step(t)
	return granted
}

func (h *modelHarness) finalizeDispatch(t modelTB, index int) {
	t.Helper()
	h.rateSlots[index]--
	h.coordinator.FinalizeDispatch(h.account(index))
	h.model.finalizeDispatch(index, h.now())
	h.coverage.note("rateSlot:dispatched")
	h.step(t)
}

func (h *modelHarness) releasePendingRateSlot(t modelTB, index int) {
	t.Helper()
	h.rateSlots[index]--
	h.coordinator.ReleasePendingRateSlot(h.account(index))
	h.model.releasePendingRateSlot(index)
	h.coverage.note("rateSlot:released")
	h.step(t)
}

func (h *modelHarness) finalizeLease(t modelTB, held *heldLease) {
	t.Helper()
	held.lease.Finalize()
	h.model.finalizeDispatch(held.index, h.now())
	held.finalized = true
	h.coverage.note("lease:finalized")
	h.step(t)
}

func (h *modelHarness) releaseLease(t modelTB, at int) {
	t.Helper()
	held := h.leases[at]

	held.lease.Release()
	h.model.releaseLease(held.index, !held.finalized)
	h.leases = append(h.leases[:at], h.leases[at+1:]...)
	if held.finalized {
		h.coverage.note("lease:releasedAfterFinalize")
	} else {
		h.coverage.note("lease:releasedOpen")
	}
	h.step(t)
}

func (h *modelHarness) incrementInFlight(t modelTB, index int) {
	t.Helper()

	got := h.coordinator.IncrementInFlight(h.account(index))
	if want := h.model.incrementInFlight(index); got != want {
		t.Fatalf("IncrementInFlight(%s) = %d, model says %d", h.account(index), got, want)
	}
	h.ownedInFlight[index]++
	h.coverage.note("inFlight:incremented")
	h.step(t)
}

func (h *modelHarness) decrementInFlight(t modelTB, index int) {
	t.Helper()

	got := h.coordinator.DecrementInFlight(h.account(index))
	if want := h.model.decrementInFlight(index); got != want {
		t.Fatalf("DecrementInFlight(%s) = %d, model says %d", h.account(index), got, want)
	}
	h.ownedInFlight[index]--
	h.coverage.note("inFlight:decremented")
	h.step(t)
}

func (h *modelHarness) apply429(t modelTB, index int, header retryAfterCase) {
	t.Helper()

	result := h.coordinator.Apply429(h.account(index), header.header, time.Time{}, false)
	want := h.model.apply429(index, h.now(), header.advance)
	if result.EnteredCooldown != want {
		t.Fatalf("Apply429(%s, %q).EnteredCooldown = %v, model says %v",
			h.account(index), header.header, result.EnteredCooldown, want)
	}
	if want {
		h.coverage.note("cooldown:entered")
	}
	h.step(t)
}

func (h *modelHarness) expireGateIfDue(t modelTB, index int) {
	t.Helper()
	account := h.model.accounts[index]
	due := account.rateGateDeadline != 0 && h.now() >= account.rateGateDeadline

	h.coordinator.ExpireGateIfDue(h.account(index))
	h.model.expireGateIfDue(index, h.now())

	if due {
		if account.health == HealthCoolingDown {
			h.coverage.note("gateExpiry:afterCooldown")
		} else {
			h.coverage.note("gateExpiry:afterSingle429")
		}
	}
	h.step(t)
}

func (h *modelHarness) disable(t modelTB, index int) {
	t.Helper()
	pinned := false
	for _, pin := range h.model.pins {
		if pin.account == h.account(index) {
			pinned = true
			break
		}
	}

	h.coordinator.Disable(h.account(index))
	h.model.disable(index)
	if pinned {
		h.coverage.note("pin:removedByDisable")
	}
	h.step(t)
}

func (h *modelHarness) selectForNewSession(t modelTB, key SessionKey, order []int) {
	t.Helper()
	h.permutation.order = order

	existing, hadPin := h.model.pins[key]
	hadProvisional := hadPin && existing.state == PinProvisional
	now, wall := h.now(), h.wall()

	result, pin := h.coordinator.SelectForNewSession(endedContext(), key)
	reserved, index, wantPin, wantFull := h.model.selectNewSessionPass(key, order, now, wall)

	if (result.Lease != nil) != reserved {
		t.Fatalf("SelectForNewSession reserved = %v, model says %v", result.Lease != nil, reserved)
	}
	if pin != wantPin {
		t.Fatalf("SelectForNewSession pin = %+v, model says %+v", pin, wantPin)
	}
	if result.PinMapFull != wantFull {
		t.Fatalf("SelectForNewSession pin map full = %v, model says %v", result.PinMapFull, wantFull)
	}
	if result.Lease != nil {
		if result.Lease.Account() != h.account(index) {
			t.Fatalf("SelectForNewSession reserved %s, model says %s", result.Lease.Account(), h.account(index))
		}
		h.leases = append(h.leases, &heldLease{lease: result.Lease, index: index})
	}

	switch {
	case hadProvisional:
		h.coverage.note("pin:provisionalAttached")
	case wantPin.Generation != 0:
		h.coverage.note("pin:provisionalInstalled")
	}
	h.step(t)
}

func (h *modelHarness) releaseProvisionalHolder(t modelTB, key SessionKey, generation uint64) {
	t.Helper()
	pin, ok := h.model.pins[key]
	last := ok && pin.state == PinProvisional && pin.generation == generation && pin.holders <= 1

	h.coordinator.ReleaseProvisionalHolder(key, generation)
	h.model.releaseProvisionalHolder(key, generation, h.wall())
	if last {
		h.coverage.note("pin:provisionalRemovedOnLastHolder")
	}
	h.step(t)
}

func (h *modelHarness) confirmPin(t modelTB, key SessionKey, index int, sequence uint64) {
	t.Helper()
	pin, ok := h.model.pins[key]
	stale := ok && sequence < pin.sequence

	h.coordinator.ConfirmPin(key, h.account(index), sequence)
	h.model.confirmPin(key, h.account(index), sequence, h.wall())
	if stale {
		h.coverage.note("pin:confirmRefusedAsStale")
	} else {
		h.coverage.note("pin:confirmed")
	}
	h.step(t)
}

func (h *modelHarness) pinAccount(t modelTB, key SessionKey) {
	t.Helper()
	pin, ok := h.model.pins[key]
	expired := ok && pin.state == PinConfirmed && !h.wall().Before(pin.expiry)

	account, live := h.coordinator.PinAccount(key)
	wantAccount, wantLive := h.model.pinAccount(key, h.wall())
	if account != wantAccount || live != wantLive {
		t.Fatalf("PinAccount = (%q, %v), model says (%q, %v)", account, live, wantAccount, wantLive)
	}
	if expired {
		h.coverage.note("pin:expiredOnRead")
	}
	h.step(t)
}

func (h *modelHarness) nextArrivalSequence(t modelTB, key SessionKey) {
	t.Helper()

	got := h.coordinator.NextArrivalSequence(key)
	if want := h.model.nextArrivalSequence(key); got != want {
		t.Fatalf("NextArrivalSequence = %d, model says %d", got, want)
	}
	h.step(t)
}

func (h *modelHarness) removePin(t modelTB, key SessionKey) {
	t.Helper()
	h.coordinator.RemovePin(key)
	h.model.removePin(key)
	h.step(t)
}

// restart replaces the coordinator with a fresh one, the way a new process
// starts: its monotonic clock begins at zero, it is under the post-start
// blackout again, and the only thing it carries over is the confirmed pins
// a store would have given it. Everything the driver owed the old
// coordinator dies with it.
func (h *modelHarness) restart(t modelTB) {
	t.Helper()
	wall := h.clock.WallNow()

	recovered := make([]RecoveredPin, 0, len(h.model.pins))
	for key, pin := range h.model.pins {
		if pin.state != PinConfirmed {
			continue
		}
		recovered = append(recovered, RecoveredPin{
			Key:     key,
			Account: pin.account,
			// A store records when the request finished; a pin's expiry is
			// that instant plus the affinity TTL, so this inverts it to
			// hand both sides the same input.
			FinishedAt: pin.expiry.Add(-policy.SessionAffinityTTL),
		})
	}

	h.clock = testsupport.NewFakeClock(wall)
	h.coordinator = NewCoordinator(modelAccountKeys, h.clock, h.permutation)
	h.model = newCoordinatorModel()
	h.leases = nil
	h.rateSlots = [3]int{}
	h.ownedInFlight = [3]int{}

	got := h.coordinator.RecoverPins(recovered)
	if want := h.model.recoverPins(recovered, wall); got != want {
		t.Fatalf("RecoverPins clamped %d pins, model says %d", got, want)
	}

	h.coverage.note("restart:performed")
	if len(recovered) > 0 {
		h.coverage.note("restart:recoveredPin")
	}
	if got > 0 {
		h.coverage.note("restart:clampedFutureFinish")
	}
	h.step(t)
}
