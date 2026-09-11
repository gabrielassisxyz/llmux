package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// A randomised walk reaching a transition is a matter of luck with the
// seed, so the coverage floor the model-based suite needs cannot be
// asserted there without going red on an innocent run. It is asserted here
// instead: one scripted sequence through the same harness, the same
// reference model and the same step-by-step comparison, written to reach
// every transition the coordinator exposes and failing by name if the
// implementation ever stops offering one of them.
//
// This is therefore two checks at once. The step-by-step agreement is
// verified exactly as in the randomised suite, and the sequence doubles as
// the proof that the reference model has an answer for every transition
// rather than for the ones a random walk happened to visit.
func TestTheReferenceModelCoversEveryTransition(t *testing.T) {
	coverage := newTransitionCoverage()
	harness := newModelHarness(coverage)
	harness.step(t)

	const k1, k2, k3 = 0, 1, 2
	fiveSeconds := retryAfterCase{header: "5", advance: 5 * time.Second}

	// Nothing dispatches during the first full rolling window of a
	// process's life, whatever else is true of the account.
	if outcome := harness.reserve(t, k1); outcome != SkippedBlackout {
		t.Fatalf("reserve during the blackout = %v, want SkippedBlackout", outcome)
	}
	harness.clock.AdvanceMonotonic(policy.PostStartDispatchBlackout + time.Second)

	// A lease finalized at the dispatch boundary still owes its in-flight
	// slot; one released while open owes both.
	if outcome := harness.reserve(t, k1); outcome != Reserved {
		t.Fatalf("first reserve after the blackout = %v, want Reserved", outcome)
	}
	harness.finalizeLease(t, harness.leases[0])
	harness.releaseLease(t, 0)

	if outcome := harness.reserve(t, k1); outcome != Reserved {
		t.Fatalf("reserve for the open-release case = %v, want Reserved", outcome)
	}
	harness.releaseLease(t, 0)

	// The in-flight counter has its own entry points, and they pair.
	harness.incrementInFlight(t, k2)
	harness.decrementInFlight(t, k2)

	// A rate-only slot ends either as a dispatch or as an explicit release,
	// and the release is the one path that frees a slot having recorded no
	// dispatch at all.
	harness.reserveRateSlot(t, k2)
	harness.releasePendingRateSlot(t, k2)
	harness.reserveRateSlot(t, k2)
	harness.finalizeDispatch(t, k2)

	// Fill one account's rolling window to the ceiling, which is the only
	// side of the rule from which a refusal and a saturated admission are
	// reachable at all.
	for range policy.DispatchesPerWindowPerAccount {
		if !harness.dispatch(t, k3) {
			t.Fatal("a dispatch inside the window ceiling was refused")
		}
	}
	if harness.dispatch(t, k3) {
		t.Fatal("the dispatch past the window ceiling was admitted")
	}
	if outcome := harness.reserve(t, k3); outcome != SkippedRateSaturated {
		t.Fatalf("reserve on a full window = %v, want SkippedRateSaturated", outcome)
	}

	// The in-flight ceiling is checked ahead of the window, so an account
	// with room in its window still refuses once twelve are live.
	for range policy.InFlightAttemptsPerAccount {
		if outcome := harness.reserve(t, k1); outcome != Reserved {
			t.Fatalf("reserve below the in-flight ceiling = %v, want Reserved", outcome)
		}
	}
	if outcome := harness.reserve(t, k1); outcome != SkippedInFlightSaturated {
		t.Fatalf("reserve at the in-flight ceiling = %v, want SkippedInFlightSaturated", outcome)
	}
	for len(harness.leases) > 0 {
		harness.releaseLease(t, 0)
	}

	// One 429 gates the account it named, and the gate's expiry leaves the
	// history alone so the cooldown count can still reach its threshold.
	harness.apply429(t, k1, fiveSeconds)
	if outcome := harness.reserve(t, k1); outcome != SkippedGate {
		t.Fatalf("reserve behind a gate = %v, want SkippedGate", outcome)
	}
	harness.clock.AdvanceMonotonic(6 * time.Second)
	harness.expireGateIfDue(t, k1)

	// Two more inside the same window reach the threshold, and this gate's
	// expiry is the other row: it returns the account to enabled and
	// clears the history.
	harness.apply429(t, k1, fiveSeconds)
	harness.apply429(t, k1, fiveSeconds)
	if health := harness.model.accounts[k1].health; health != HealthCoolingDown {
		t.Fatalf("health after the third 429 = %v, want HealthCoolingDown", health)
	}
	harness.clock.AdvanceMonotonic(policy.RollingRateWindow + time.Second)
	harness.expireGateIfDue(t, k1)
	if health := harness.model.accounts[k1].health; health != HealthEnabled {
		t.Fatalf("health after the cooldown gate expired = %v, want HealthEnabled", health)
	}

	// A new session installs a provisional pin with its reservation; a
	// second request attaches to it rather than choosing its own account;
	// and the pin goes the moment its last holder lets go.
	sessionA := modelSessionKey(0)
	harness.selectForNewSession(t, sessionA, []int{k1, k2, k3})
	harness.selectForNewSession(t, sessionA, []int{k1, k2, k3})
	harness.releaseProvisionalHolder(t, sessionA, 1)
	harness.releaseProvisionalHolder(t, sessionA, 1)
	if _, ok := harness.model.pins[sessionA]; ok {
		t.Fatal("the provisional pin outlived its last holder")
	}
	for len(harness.leases) > 0 {
		harness.releaseLease(t, 0)
	}

	// A confirmation earns the hour of affinity, an older arrival cannot
	// overwrite it, and the pin is removed lazily by the read that finds it
	// expired rather than by any sweep.
	sessionB := modelSessionKey(1)
	harness.confirmPin(t, sessionB, k1, 2)
	harness.confirmPin(t, sessionB, k1, 1)
	harness.nextArrivalSequence(t, sessionB)
	harness.pinAccount(t, sessionB)
	harness.clock.AdvanceWall(policy.SessionAffinityTTL + time.Second)
	harness.pinAccount(t, sessionB)
	if _, ok := harness.model.pins[sessionB]; ok {
		t.Fatal("the expired pin survived the read that found it expired")
	}

	// Disabling an account takes its pins with it and is terminal.
	sessionC := modelSessionKey(2)
	harness.confirmPin(t, sessionC, k2, 1)
	harness.disable(t, k2)
	if outcome := harness.reserve(t, k2); outcome != SkippedDisabled {
		t.Fatalf("reserve on a disabled account = %v, want SkippedDisabled", outcome)
	}
	harness.removePin(t, sessionC)

	// A restart carries confirmed pins across and nothing else.
	harness.confirmPin(t, sessionB, k1, 1)
	harness.restart(t)
	if harness.model.accounts[k2].health != HealthEnabled {
		t.Fatal("a disabled account survived the restart that was supposed to clear it")
	}

	// The wall clock moving backwards is the one case that makes a recorded
	// finish instant lie in the future, which recovery clamps.
	harness.clock.AdvanceWall(-2 * policy.SessionAffinityTTL)
	harness.restart(t)

	coverage.assertComplete(t)
}
