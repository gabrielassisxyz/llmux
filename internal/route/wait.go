package route

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// WaitToken is a snapshot of the coordinator's notification channel,
// captured under the lock at the moment a caller decides to wait. Waiting
// on it after releasing the lock cannot miss a state change that happened
// before the token was captured: any mutation between capture and wait has
// already closed this exact channel, and a select on it returns
// immediately instead of blocking.
type WaitToken <-chan struct{}

// WaitOutcome reports why Wait returned.
type WaitOutcome int

const (
	// WaitNotified means a coordinator mutation may have made a
	// previously ineligible account eligible; the caller must recheck
	// eligibility under the lock rather than assume admission.
	WaitNotified WaitOutcome = iota
	// WaitTimedOut means the account-acquisition ceiling elapsed with no
	// notification.
	WaitTimedOut
	// WaitCanceled means the caller's context ended first.
	WaitCanceled
)

// WaitToken returns the coordinator's current notification token. Call it
// while still holding the lock, in the same critical section as the
// eligibility check that decided to wait: capturing it after unlocking
// reopens exactly the race this type exists to close.
func (c *Coordinator) WaitToken() WaitToken {
	return c.notify
}

// Wait blocks until token fires, ctx ends, or the fixed account-acquisition
// ceiling elapses, whichever comes first. A single account-selection phase
// must never wait longer than that ceiling even when the caller's own
// context has more time remaining, so the ceiling is enforced here rather
// than left to the caller to remember. Wait takes no lock and performs no
// I/O: it is a pure synchronization primitive over a channel and a timer
// captured elsewhere.
func (c *Coordinator) Wait(ctx context.Context, token WaitToken) WaitOutcome {
	timer := c.clk.NewTimer(policy.MaxAccountAcquisitionTime)
	defer timer.Stop()

	select {
	case <-token:
		return WaitNotified
	case <-ctx.Done():
		return WaitCanceled
	case <-timer.C():
		return WaitTimedOut
	}
}

// notifyLocked wakes every current waiter and installs a fresh
// notification channel. The caller must hold c.mu. Every mutation this
// package makes to state a waiter might be blocked on must end with a call
// to notifyLocked before returning, the same way every such mutation must
// happen under the lock in the first place.
func (c *Coordinator) notifyLocked() {
	close(c.notify)
	c.notify = make(chan struct{})
}
