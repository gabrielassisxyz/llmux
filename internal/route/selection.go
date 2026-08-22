package route

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

// SelectionOutcome reports the terminal result of an account-selection phase.
type SelectionOutcome int

const (
	// SelectionReserved means a lease was acquired.
	SelectionReserved SelectionOutcome = iota
	// SelectionAllDisabled means every account is disabled, so the phase
	// answers local 503 immediately rather than waiting.
	SelectionAllDisabled
	// SelectionCapacityTimeout means the account-acquisition ceiling
	// elapsed with no account becoming eligible.
	SelectionCapacityTimeout
	// SelectionCanceled means the caller's context ended first.
	SelectionCanceled
)

// SkipDecision records one account's local skip reason during a selection
// pass. It is returned so the caller can persist it as a durable skip row;
// the route package performs no I/O itself.
type SkipDecision struct {
	Account catalog.Account
	Reason  ReservationOutcome
}

// SelectionResult is the outcome of one account-selection phase.
type SelectionResult struct {
	Lease   *PendingLease
	Skips   []SkipDecision
	Outcome SelectionOutcome

	// SpillFrom is the account a session was pinned to when the selection
	// spilled to a different account. It is empty when the selection was
	// not a spill, including when the pinned account itself won.
	SpillFrom catalog.Account

	// PinMapFull is true when a new session was refused a pin because the
	// pin map is at its ceiling. The caller logs it; it is not a failure.
	PinMapFull bool
}

// Select acquires a lease for an unpinned base alias by considering the
// three accounts in a freshly generated permutation order, reshuffling after
// every wake so a repeatedly-woken request does not keep preferring the same
// account. It returns the first eligible account, or a terminal outcome when
// no account can be acquired.
func (c *Coordinator) Select(ctx context.Context) SelectionResult {
	var skips []SkipDecision
	for {
		order := c.perm.Perm(3)
		lease, passSkips, allDisabled, token := c.selectOnce(order)
		skips = append(skips, passSkips...)
		if lease != nil {
			return SelectionResult{Lease: lease, Skips: skips, Outcome: SelectionReserved}
		}
		if allDisabled {
			return SelectionResult{Skips: skips, Outcome: SelectionAllDisabled}
		}

		switch c.Wait(ctx, token) {
		case WaitNotified:
			continue
		case WaitCanceled:
			return SelectionResult{Skips: skips, Outcome: SelectionCanceled}
		case WaitTimedOut:
			return SelectionResult{Skips: skips, Outcome: SelectionCapacityTimeout}
		}
	}
}

// selectOnce performs one pass over the permutation order under a single
// lock hold, reserving the first eligible account. When no account is
// eligible it reports whether every account is disabled, and otherwise
// returns the notification token to wait on, captured under the same lock
// hold as the eligibility checks so no state change can be missed.
func (c *Coordinator) selectOnce(order []int) (lease *PendingLease, skips []SkipDecision, allDisabled bool, token WaitToken) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var sawRecoverable bool
	for _, idx := range order {
		account := accountFromIndex(idx)
		l, outcome := c.reserveLocked(account)
		if l != nil {
			return l, skips, false, nil
		}
		skips = append(skips, SkipDecision{Account: account, Reason: outcome})
		if outcome != SkippedDisabled {
			sawRecoverable = true
		}
	}

	if !sawRecoverable {
		return nil, skips, true, nil
	}
	return nil, skips, false, c.WaitToken()
}
