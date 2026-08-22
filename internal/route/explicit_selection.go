package route

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

// SelectExplicit acquires a lease for an exact -kN alias, which names one
// account and gives up every fallback the flexible route has. It considers
// only the named account, never spills, and waits for temporary rate,
// in-flight or cooldown capacity for at most the account-acquisition
// ceiling. A disabled named account fails immediately with no wait and no
// probe: a disabled account stays disabled for the process lifetime, and
// restart is the entire recovery mechanism.
//
// The returned SelectionResult reuses the flexible selection's terminal
// outcomes: SelectionAllDisabled means the named account is disabled (the
// only eligible account is unusable), SelectionCapacityTimeout means the
// ceiling elapsed, and SelectionCanceled means the caller's context ended.
func (c *Coordinator) SelectExplicit(ctx context.Context, account catalog.Account) SelectionResult {
	var skips []SkipDecision
	for {
		lease, skip, disabled, token := c.selectExplicitOnce(account)
		if lease != nil {
			return SelectionResult{Lease: lease, Skips: skips, Outcome: SelectionReserved}
		}
		skips = append(skips, skip)
		if disabled {
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

// selectExplicitOnce performs one admission attempt for the named account
// under a single lock hold. It reports whether the account is disabled, so
// the caller can answer 503 immediately rather than waiting, and otherwise
// returns the notification token to wait on, captured under the same lock
// hold as the eligibility check so no state change can be missed.
func (c *Coordinator) selectExplicitOnce(account catalog.Account) (lease *PendingLease, skip SkipDecision, disabled bool, token WaitToken) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lease, outcome := c.reserveLocked(account)
	if lease != nil {
		return lease, SkipDecision{}, false, nil
	}
	if outcome == SkippedDisabled {
		return nil, SkipDecision{Account: account, Reason: outcome}, true, nil
	}
	return nil, SkipDecision{Account: account, Reason: outcome}, false, c.WaitToken()
}
