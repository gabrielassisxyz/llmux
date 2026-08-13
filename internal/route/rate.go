package route

import (
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// ReserveRateSlot prunes account's dispatch-timestamp deque to the current
// rolling 60-second window and, if the remaining timestamps plus pending
// reservations have not already reached the per-account ceiling, reserves
// a pending slot and returns true.
//
// Every account is refused unconditionally for one full rolling window
// after process start, measured on the monotonic clock from its own zero
// point. A restarted process reads no rate state from the store, so this
// is the only thing standing between it and exceeding the ceiling it
// claims to enforce: every dispatch of the previous process precedes that
// process's death, every dispatch of this one follows this one's start by
// a full window, and no 60-second interval can contain both.
//
// This checks the rate window and the blackout only. It does not touch
// in-flight or health; a caller composes those separately before treating
// an account as admitted.
func (c *Coordinator) ReserveRateSlot(account catalog.Account) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.clk.MonotonicNow() < policy.PostStartDispatchBlackout {
		return false
	}

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
// the rolling rate window. The caller must hold c.mu.
func (c *Coordinator) pruneRateWindowLocked(state *accountState) {
	state.dispatchTimestamps = pruneOlderThanLocked(state.dispatchTimestamps, c.clk.MonotonicNow()-policy.RollingRateWindow)
}

// pruneOlderThanLocked removes entries at or before cutoff from an
// ascending slice of monotonic instants, so pruning is a single scan from
// the front. Shared by the dispatch-timestamp deque and the recent-429
// history, which prune the same way against different cutoffs.
func pruneOlderThanLocked(timestamps []time.Duration, cutoff time.Duration) []time.Duration {
	prune := 0
	for prune < len(timestamps) && timestamps[prune] <= cutoff {
		prune++
	}
	if prune == 0 {
		return timestamps
	}
	return timestamps[prune:]
}
