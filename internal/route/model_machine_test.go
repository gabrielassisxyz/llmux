package route

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// The example-based tests in this package cover the interleavings somebody
// thought to write down. This one covers the ones nobody did: it drives the
// real Coordinator and the reference model in model_test.go with the same
// randomly generated sequence of operations and compares the two after every
// single step, so a divergence is reported at the operation that caused it
// rather than at the end of a run.
//
// When a sequence fails, it is shrunk to a minimal reproducer, its seed is
// printed, and the recorded draws are written under testdata/rapid/ so the
// same failure can be replayed while the defect is open. That file is a
// reproduction aid rather than a regression fixture, and the difference
// matters: a recorded failing prefix is shorter than a passing sequence, so
// once the defect is fixed the same recording runs out of draws partway
// through the walk and is reported as no longer valid. The durable half of
// the protocol is therefore deliberate rather than automatic. Transcribe the
// shrunk sequence into a named example test in the file that owns the rule it
// broke, which is where TestLeaseFinalizeAnchorsTheWindowAtFinalizeNotReservation
// came from.

// retryAfterCase pairs a Retry-After header with the gate advance the rules
// derive from it. Stating the expected advance here keeps the model
// independent of the header parser, which has unit tests of its own: the
// cases below exercise the absent, zero, negative, unparseable, ordinary and
// above-the-clamp readings.
type retryAfterCase struct {
	header  string
	advance time.Duration
}

var retryAfterCases = []retryAfterCase{
	{header: "", advance: time.Second},
	{header: "garbage", advance: time.Second},
	{header: "0", advance: time.Second},
	{header: "-5", advance: time.Second},
	{header: "1", advance: time.Second},
	{header: "5", advance: 5 * time.Second},
	{header: "30", advance: 30 * time.Second},
	{header: "120", advance: 2 * time.Minute},
	{header: "1200", advance: 20 * time.Minute},
}

// clockSteps are the advances the generator may make. They straddle every
// boundary the rules name: the one-second floor, the five-second grace, the
// sixty-second window and blackout, the ten-minute clamp, and the one-hour
// session affinity TTL. A step table that cannot cross an interval can never
// reach that interval's expiry.
var clockSteps = []time.Duration{
	0,
	time.Second,
	5 * time.Second,
	30 * time.Second,
	59 * time.Second,
	time.Minute,
	61 * time.Second,
	10 * time.Minute,
	policy.SessionAffinityTTL,
}

var candidateOrders = [][]int{
	{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0},
}

func modelSessionKey(n int) SessionKey {
	var key SessionKey
	key[0] = 1
	key[1] = byte(n)
	return key
}

func TestCoordinatorAgreesWithTheReferenceModel(t *testing.T) {
	// The walk records coverage the way the scripted scenario does, but the
	// floor is asserted only there: which transitions a random sequence
	// reaches varies with the seed, so a coverage gate here would go red on
	// an innocent run and be switched off within a week.
	coverage := newTransitionCoverage()

	rapid.Check(t, func(t *rapid.T) {
		harness := newModelHarness(coverage)
		drawAccount := func(t *rapid.T) int { return rapid.IntRange(0, 2).Draw(t, "account") }
		drawSession := func(t *rapid.T) SessionKey {
			return modelSessionKey(rapid.IntRange(0, 2).Draw(t, "session"))
		}

		t.Repeat(map[string]func(*rapid.T){
			"advanceMonotonic": func(t *rapid.T) {
				harness.clock.AdvanceMonotonic(rapid.SampledFrom(clockSteps).Draw(t, "step"))
			},

			// Wall time moves on its own, and backwards, because every rule
			// that reads it claims to be independent of the monotonic clock
			// and only a separate advance can test that.
			"advanceWall": func(t *rapid.T) {
				step := rapid.SampledFrom(clockSteps).Draw(t, "step")
				if rapid.Bool().Draw(t, "backwards") {
					step = -step
				}
				harness.clock.AdvanceWall(step)
			},

			"reserve": func(t *rapid.T) {
				harness.reserve(t, drawAccount(t))
			},

			// A uniform choice among twenty-odd operations never takes one of
			// them twelve times running, so the in-flight ceiling would be
			// unreachable without a burst that can.
			"reserveBurst": func(t *rapid.T) {
				index := drawAccount(t)
				count := rapid.IntRange(2, policy.InFlightAttemptsPerAccount+2).Draw(t, "count")
				for range count {
					harness.reserve(t, index)
				}
			},

			// Same reasoning for the rolling window, which is sixty
			// dispatches deep, plus the admission that follows it: the
			// rate-saturated branch of the rule only exists on the far side
			// of a full window.
			"fillWindowThenAdmit": func(t *rapid.T) {
				index := drawAccount(t)
				count := rapid.IntRange(2, policy.DispatchesPerWindowPerAccount+2).Draw(t, "count")
				for range count {
					harness.dispatch(t, index)
				}
				harness.reserve(t, index)
			},

			"finalizeLease": func(t *rapid.T) {
				open := make([]int, 0, len(harness.leases))
				for i, held := range harness.leases {
					if !held.finalized {
						open = append(open, i)
					}
				}
				if len(open) == 0 {
					t.Skip("no open lease held")
				}
				harness.finalizeLease(t, harness.leases[rapid.SampledFrom(open).Draw(t, "lease")])
			},

			"releaseLease": func(t *rapid.T) {
				if len(harness.leases) == 0 {
					t.Skip("no lease held")
				}
				harness.releaseLease(t, rapid.IntRange(0, len(harness.leases)-1).Draw(t, "lease"))
			},

			"reserveRateSlot": func(t *rapid.T) {
				harness.reserveRateSlot(t, drawAccount(t))
			},

			"finalizeDispatch": func(t *rapid.T) {
				harness.finalizeDispatch(t, drawOwedRateSlot(t, harness))
			},

			"releasePendingRateSlot": func(t *rapid.T) {
				harness.releasePendingRateSlot(t, drawOwedRateSlot(t, harness))
			},

			// The ceiling is the caller's to respect at this entry point,
			// which is why the operation is skipped rather than generating a
			// call whose result no rule defines.
			"incrementInFlight": func(t *rapid.T) {
				index := drawAccount(t)
				if harness.model.accounts[index].inFlight >= policy.InFlightAttemptsPerAccount {
					t.Skip("in-flight ceiling reached")
				}
				harness.incrementInFlight(t, index)
			},

			"decrementInFlight": func(t *rapid.T) {
				index := drawAccount(t)
				if harness.ownedInFlight[index] == 0 {
					t.Skip("no in-flight slot owed")
				}
				harness.decrementInFlight(t, index)
			},

			// The count is drawn rather than fixed at one because the
			// cooldown circuit opens on the third 429 inside one window
			// against the same account, and a uniform walk does not pick one
			// operation three times running.
			"apply429": func(t *rapid.T) {
				index := drawAccount(t)
				count := rapid.IntRange(1, cooldownThreshold+1).Draw(t, "count")
				for range count {
					harness.apply429(t, index, rapid.SampledFrom(retryAfterCases).Draw(t, "retryAfter"))
				}
			},

			"expireGateIfDue": func(t *rapid.T) {
				harness.expireGateIfDue(t, drawAccount(t))
			},

			"disable": func(t *rapid.T) {
				harness.disable(t, drawAccount(t))
			},

			"selectForNewSession": func(t *rapid.T) {
				harness.selectForNewSession(t, drawSession(t),
					rapid.SampledFrom(candidateOrders).Draw(t, "order"))
			},

			"releaseProvisionalHolder": func(t *rapid.T) {
				harness.releaseProvisionalHolder(t, drawSession(t),
					rapid.SampledFrom([]uint64{0, 1, 2}).Draw(t, "generation"))
			},

			"confirmPin": func(t *rapid.T) {
				harness.confirmPin(t, drawSession(t), drawAccount(t),
					rapid.SampledFrom([]uint64{0, 1, 2, 3}).Draw(t, "sequence"))
			},

			"pinAccount": func(t *rapid.T) {
				harness.pinAccount(t, drawSession(t))
			},

			"nextArrivalSequence": func(t *rapid.T) {
				harness.nextArrivalSequence(t, drawSession(t))
			},

			"removePin": func(t *rapid.T) {
				harness.removePin(t, drawSession(t))
			},

			"restart": func(t *rapid.T) {
				harness.restart(t)
			},

			"": func(t *rapid.T) {
				harness.step(t)
			},
		})
	})
}

func drawOwedRateSlot(t *rapid.T, harness *modelHarness) int {
	t.Helper()
	owed := make([]int, 0, len(harness.rateSlots))
	for index, count := range harness.rateSlots {
		if count > 0 {
			owed = append(owed, index)
		}
	}
	if len(owed) == 0 {
		t.Skip("no rate slot held")
	}
	return rapid.SampledFrom(owed).Draw(t, "slot")
}
