package route

import (
	"context"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// pinStallOutcome reports how the reopen-aware stall for a session's pinned
// account ended.
type pinStallOutcome int

const (
	// pinStallReserved means the pinned account became eligible within the
	// grace and a lease was acquired.
	pinStallReserved pinStallOutcome = iota
	// pinStallSpill means the pin could not be preserved and the caller
	// should select among all accounts, marking a spill when an
	// alternative wins.
	pinStallSpill
	// pinStallCanceled means the caller's context ended during the stall.
	pinStallCanceled
)

// pinBlockage describes why a pinned account is not eligible and when it is
// next known to reopen. It is computed under the coordinator lock in the
// same critical section as the admission attempt, so the two cannot
// disagree.
type pinBlockage struct {
	// disabled means the account is disabled for the process lifetime.
	disabled bool

	// reopen is the latest deterministic reopening instant among the
	// blackout, the rate gate and the RPM window, on the monotonic clock.
	// It is meaningful only when hasReopen is true.
	reopen    time.Duration
	hasReopen bool
}

// considerReopen keeps the latest of the deterministic reopening instants,
// because every deterministic blocker must clear before the account is
// eligible again.
func (b *pinBlockage) considerReopen(d time.Duration) {
	if !b.hasReopen || d > b.reopen {
		b.reopen = d
		b.hasReopen = true
	}
}

// pinBlockageLocked inspects the pinned account's deterministic blockers.
// The caller must hold c.mu. In-flight saturation is deliberately absent:
// its release time is unknowable, so it contributes no reopening instant
// and the stall waits on notification instead.
func (c *Coordinator) pinBlockageLocked(account catalog.Account) pinBlockage {
	now := c.clk.MonotonicNow()
	state := &c.accounts[accountIndex(account)]

	b := pinBlockage{disabled: state.health == HealthDisabled}

	if now < policy.PostStartDispatchBlackout {
		b.considerReopen(policy.PostStartDispatchBlackout)
	}
	if state.rateGateDeadline != 0 && now < state.rateGateDeadline {
		b.considerReopen(state.rateGateDeadline)
	}
	if len(state.dispatchTimestamps) >= policy.DispatchesPerWindowPerAccount {
		b.considerReopen(state.dispatchTimestamps[0] + policy.RollingRateWindow)
	}

	return b
}

// pinAttempt captures one admission attempt for a session's pinned account
// under a single lock hold.
type pinAttempt struct {
	lease *PendingLease
	skip  SkipDecision

	// pinGone reports that the pin no longer exists or no longer points at
	// the account the stall started with, so the caller should fall through
	// to unpinned selection.
	pinGone bool

	// disabled reports that the pin's account is disabled and the pin was
	// removed.
	disabled bool

	token    WaitToken
	blockage pinBlockage
}

// tryPinnedAccountLocked attempts to reserve the pinned account and, when
// it is not eligible, reports the skip reason, whether the pin is disabled,
// and the deterministic reopening instant, all under one lock hold.
func (c *Coordinator) tryPinnedAccountLocked(key SessionKey, pinAccount catalog.Account) pinAttempt {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if !ok || pin.state != PinConfirmed || pin.account != pinAccount {
		return pinAttempt{pinGone: true}
	}
	if !c.clk.WallNow().Before(pin.expiry) {
		delete(c.pins, key)
		return pinAttempt{pinGone: true}
	}

	lease, outcome := c.reserveLocked(pinAccount)
	if lease != nil {
		return pinAttempt{lease: lease}
	}
	if outcome == SkippedDisabled {
		delete(c.pins, key)
		return pinAttempt{skip: SkipDecision{Account: pinAccount, Reason: outcome}, disabled: true}
	}
	return pinAttempt{
		skip:     SkipDecision{Account: pinAccount, Reason: outcome},
		token:    c.WaitToken(),
		blockage: c.pinBlockageLocked(pinAccount),
	}
}

// pinDeadline returns the monotonic instant at which the pin stall must
// end: the earlier of the five-second grace and the logical request
// deadline. The 60-second acquisition ceiling never binds here because the
// grace is always shorter, and the stall runs at the start of the
// acquisition phase.
func (c *Coordinator) pinDeadline(ctx context.Context) time.Duration {
	nowMono := c.clk.MonotonicNow()
	deadline := nowMono + policy.SaturatedPinGrace

	if wallDeadline, ok := ctx.Deadline(); ok {
		nowWall := c.clk.WallNow()
		remaining := wallDeadline.Sub(nowWall)
		if remaining < 0 {
			remaining = 0
		}
		if bound := nowMono + remaining; bound < deadline {
			deadline = bound
		}
	}
	return deadline
}

// waitForPin blocks until token fires, ctx ends, or the monotonic instant
// waitUntil arrives, whichever comes first. It reports false only when ctx
// ended; a notification and a timer both mean the caller should re-inspect
// the pin. It takes no lock and performs no I/O.
func (c *Coordinator) waitForPin(ctx context.Context, token WaitToken, waitUntil time.Duration) bool {
	now := c.clk.MonotonicNow()
	if now >= waitUntil {
		return true
	}
	timer := c.clk.NewTimer(waitUntil - now)
	defer timer.Stop()

	select {
	case <-token:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C():
		return true
	}
}

// stallForPin implements the reopen-aware bounded stall for a session's
// pinned account. It returns a lease when the pin becomes eligible within
// the grace, or a spill decision when the pin cannot be preserved. The
// caller owns the spill marking: it selects among all accounts and records
// the original pin as the spill source when an alternative wins.
func (c *Coordinator) stallForPin(ctx context.Context, key SessionKey, pinAccount catalog.Account) (lease *PendingLease, skip SkipDecision, outcome pinStallOutcome) {
	pinDeadline := c.pinDeadline(ctx)

	for {
		attempt := c.tryPinnedAccountLocked(key, pinAccount)
		if attempt.lease != nil {
			return attempt.lease, SkipDecision{}, pinStallReserved
		}
		if attempt.pinGone {
			return nil, SkipDecision{}, pinStallSpill
		}
		if attempt.disabled {
			return nil, attempt.skip, pinStallSpill
		}

		now := c.clk.MonotonicNow()
		if now >= pinDeadline {
			return nil, attempt.skip, pinStallSpill
		}

		waitUntil := pinDeadline
		if attempt.blockage.hasReopen {
			if attempt.blockage.reopen >= pinDeadline {
				// A deterministic blocker beyond the grace makes waiting
				// pointless: spill immediately rather than burn the grace.
				return nil, attempt.skip, pinStallSpill
			}
			waitUntil = attempt.blockage.reopen
		}

		if !c.waitForPin(ctx, attempt.token, waitUntil) {
			return nil, attempt.skip, pinStallCanceled
		}
	}
}
