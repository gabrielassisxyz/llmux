package route

import (
	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// ReserveRateSlot prunes account's dispatch-timestamp deque to the current
// rolling 60-second window and, if the remaining timestamps plus pending
// reservations have not already reached the per-account ceiling, reserves
// a pending slot and returns true.
//
// This checks the rate window only. It does not touch in-flight, health,
// or the post-start dispatch blackout; a caller composes those separately
// before treating an account as admitted.
func (c *Coordinator) ReserveRateSlot(account catalog.Account) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	c.pruneRateWindowLocked(state)

	if len(state.dispatchTimestamps)+state.pendingReservations >= policy.DispatchesPerWindowPerAccount {
		return false
	}
	state.pendingReservations++
	return true
}

// ReleasePendingRateSlot frees a pending reservation whose admission
// commit failed and will therefore never call FinalizeDispatch. It is the
// one path that frees a slot without ever recording a dispatch, so it
// wakes waiters who might now fit in the freed capacity.
func (c *Coordinator) ReleasePendingRateSlot(account catalog.Account) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	if state.pendingReservations > 0 {
		state.pendingReservations--
	}
	c.notifyLocked()
}

// FinalizeDispatch moves account's reservation from pending into the
// rolling window: it decrements the pending count and appends the current
// monotonic instant to the dispatch-timestamp deque. Call it immediately
// before http.Client.Do and never before, so the window is anchored at the
// call it authorizes rather than at the reservation that preceded it. A
// timestamp installed here is never refunded, even if the dispatch that
// follows fails, is canceled, or is retried.
func (c *Coordinator) FinalizeDispatch(account catalog.Account) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	if state.pendingReservations > 0 {
		state.pendingReservations--
	}
	state.dispatchTimestamps = append(state.dispatchTimestamps, c.clk.MonotonicNow())
}

// pruneRateWindowLocked removes dispatch timestamps at or before now minus
// the rolling rate window. The caller must hold c.mu. dispatchTimestamps
// is ascending, so pruning is a single scan from the front.
func (c *Coordinator) pruneRateWindowLocked(state *accountState) {
	cutoff := c.clk.MonotonicNow() - policy.RollingRateWindow
	prune := 0
	for prune < len(state.dispatchTimestamps) && state.dispatchTimestamps[prune] <= cutoff {
		prune++
	}
	if prune > 0 {
		state.dispatchTimestamps = state.dispatchTimestamps[prune:]
	}
}
