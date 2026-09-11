package route

import (
	"sort"
	"strings"
	"testing"
)

// modelTB is the slice of the testing interface the shared harness needs.
// It exists so one comparison routine serves both the randomised walk,
// which runs under rapid, and the scripted scenario, which runs under the
// ordinary testing package.
type modelTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Skip(args ...any)
}

// A random walk that happens to never reach a ceiling, never open the
// cooldown circuit, or never let the last holder of a provisional pin go
// has proven nothing about those rules while still reporting a pass. The
// coverage ledger below turns "the generated sequences reach every
// transition" from a hope into an assertion: the driver records each
// transition it drove, and the test fails at the end naming any required
// one that never happened.
type transitionCoverage struct {
	seen map[string]int
}

func newTransitionCoverage() *transitionCoverage {
	return &transitionCoverage{seen: map[string]int{}}
}

func (coverage *transitionCoverage) note(transition string) {
	coverage.seen[transition]++
}

// requiredTransitions is every state transition the coordinator exposes
// that a model-based suite has to have exercised for its pass to mean
// anything. Each name is recorded by the driver at the point the model
// decides that transition happened.
var requiredTransitions = []string{
	"reserve:granted",
	"reserve:blackout",
	"reserve:disabled",
	"reserve:gate",
	"reserve:inFlightSaturated",
	"reserve:rateSaturated",
	"cooldown:entered",
	"gateExpiry:afterCooldown",
	"gateExpiry:afterSingle429",
	"inFlight:incremented",
	"inFlight:decremented",
	"rateSlot:granted",
	"rateSlot:refused",
	"rateSlot:dispatched",
	"rateSlot:released",
	"lease:finalized",
	"lease:releasedOpen",
	"lease:releasedAfterFinalize",
	"pin:provisionalInstalled",
	"pin:provisionalAttached",
	"pin:provisionalRemovedOnLastHolder",
	"pin:confirmed",
	"pin:confirmRefusedAsStale",
	"pin:expiredOnRead",
	"pin:removedByDisable",
	"restart:performed",
	"restart:recoveredPin",
	"restart:clampedFutureFinish",
}

func (coverage *transitionCoverage) assertComplete(t *testing.T) {
	t.Helper()

	var missing []string
	for _, transition := range requiredTransitions {
		if coverage.seen[transition] == 0 {
			missing = append(missing, transition)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Fatalf("the generated sequences never reached %d transition(s), so a pass proves nothing about them:\n  %s",
		len(missing), strings.Join(missing, "\n  "))
}

// reservationOutcomeName names an outcome for the coverage ledger. It lives
// here rather than as a String method on ReservationOutcome so that nothing
// in production grows a method only tests read.
func reservationOutcomeName(outcome ReservationOutcome) string {
	switch outcome {
	case Reserved:
		return "granted"
	case SkippedBlackout:
		return "blackout"
	case SkippedDisabled:
		return "disabled"
	case SkippedGate:
		return "gate"
	case SkippedInFlightSaturated:
		return "inFlightSaturated"
	case SkippedRateSaturated:
		return "rateSaturated"
	default:
		return "unknown"
	}
}
