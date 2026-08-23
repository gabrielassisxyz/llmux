package route

import (
	"context"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/errcode"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/store"
)

// EarliestReopen returns the earliest wall-clock instant at which any
// account is next known to become eligible. It is the all-accounts form of
// EarliestReopenFor, used by the flexible selection path.
func (c *Coordinator) EarliestReopen() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.earliestReopenLocked([]catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3})
}

// EarliestReopenFor returns the earliest wall-clock instant at which the
// named account is next known to become eligible. It is the single-account
// form of EarliestReopen, used by the explicit -kN path whose Retry-After
// reads only the named account's reopening.
func (c *Coordinator) EarliestReopenFor(account catalog.Account) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.earliestReopenLocked([]catalog.Account{account})
}

// earliestReopenLocked computes the earliest wall-clock reopening among the
// given accounts, considering the post-start dispatch blackout, each
// account's rate gate, and each account's rolling RPM window. The caller
// must hold c.mu. It returns ok=false when no account has a known reopening
// time, which is the in-flight-only case the error envelope answers with a
// one-second fallback. Disabled accounts contribute nothing: a disabled
// account stays disabled for the process lifetime.
//
// The reopening instants are monotonic in the account state, so the result
// is converted to wall time by the same offset the injected clock reports
// between its two reads. A wall-clock step between the reads shifts the
// answer by the step, which is the same cost a wall-clock Retry-After
// always pays.
func (c *Coordinator) earliestReopenLocked(accounts []catalog.Account) (time.Time, bool) {
	nowMono := c.clk.MonotonicNow()
	nowWall := c.clk.WallNow()

	var earliest time.Duration
	found := false
	consider := func(d time.Duration) {
		if !found || d < earliest {
			earliest = d
			found = true
		}
	}

	if nowMono < policy.PostStartDispatchBlackout {
		consider(policy.PostStartDispatchBlackout)
	}

	for _, account := range accounts {
		state := &c.accounts[accountIndex(account)]
		if state.health == HealthDisabled {
			continue
		}
		if state.rateGateDeadline != 0 && nowMono < state.rateGateDeadline {
			consider(state.rateGateDeadline)
		}
		if len(state.dispatchTimestamps) >= policy.DispatchesPerWindowPerAccount {
			consider(state.dispatchTimestamps[0] + policy.RollingRateWindow)
		}
	}

	if !found {
		return time.Time{}, false
	}
	return nowWall.Add(earliest - nowMono), true
}

// SkipReasonFor maps a reservation skip outcome to the store's skip-reason
// vocabulary. Reserved is not a skip outcome and never reaches a
// selection-failure phase; the default branch exists only so an invalid
// label degrades to a defined value rather than an empty string.
func SkipReasonFor(outcome ReservationOutcome) store.SkipReason {
	switch outcome {
	case SkippedBlackout:
		return store.SkipReasonStartBlackout
	case SkippedDisabled:
		return store.SkipReasonDisabled
	case SkippedGate:
		return store.SkipReasonRateGated
	case SkippedInFlightSaturated:
		return store.SkipReasonInFlightLimit
	case SkippedRateSaturated:
		return store.SkipReasonRPMLimit
	default:
		return store.SkipReasonRPMLimit
	}
}

// SelectionFailure is the terminal result of a selection phase that
// acquired no lease. Code is the local error code the phase answers with;
// RetryAfter is the wall-clock reopening instant, meaningful only when Code
// is a 429 code and zero when the only blockers are in-flight slots; Outcome
// is the store outcome the phase's terminal row records.
type SelectionFailure struct {
	Code       errcode.ErrorCode
	RetryAfter time.Time
	Outcome    store.Outcome
}

// ClassifySelectionFailure maps a terminal selection result to the local
// error code, Retry-After reopening, and store outcome the phase records.
// ctxErr is the context's error at the moment selection gave up; it
// distinguishes a logical-deadline expiry (504) from client cancellation
// (no response, client_canceled). It must be called only with a result
// whose Lease is nil.
func (c *Coordinator) ClassifySelectionFailure(result SelectionResult, ctxErr error) SelectionFailure {
	switch result.Outcome {
	case SelectionAllDisabled:
		return SelectionFailure{Code: errcode.ErrAccountUnavailable, Outcome: store.OutcomeNoAccountAvailable}
	case SelectionCapacityTimeout:
		reopen, _ := c.EarliestReopen()
		return SelectionFailure{Code: errcode.ErrAccountCapacityTimeout, RetryAfter: reopen, Outcome: store.OutcomeCapacityTimeout}
	case SelectionCanceled:
		if ctxErr == context.DeadlineExceeded {
			return SelectionFailure{Code: errcode.ErrDeadlineExceeded, Outcome: store.OutcomeDeadlineExceeded}
		}
		return SelectionFailure{Outcome: store.OutcomeClientCanceled}
	default:
		return SelectionFailure{}
	}
}
