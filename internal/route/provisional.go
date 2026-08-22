package route

import (
	"context"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

// ProvisionalPin identifies the provisional pin a request acquired for a
// new session. Generation is zero when no pin was acquired.
type ProvisionalPin struct {
	Account    catalog.Account
	Sequence   uint64
	Generation uint64
}

// SelectForNewSession acquires a lease for a new session, atomically
// installing (or attaching to) the provisional pin. The first request
// reserves an account and installs the pin in one critical section, so
// concurrent first requests cannot independently choose different initial
// accounts; a concurrent request attaches to the same pin and reserves the
// same account.
//
// The returned ProvisionalPin carries the request's arrival sequence (for
// ConfirmPin on success) and the provisional generation (for
// ReleaseProvisionalHolder on failure). Generation is zero when no pin was
// acquired, which happens only when the selection phase itself failed.
func (c *Coordinator) SelectForNewSession(ctx context.Context, key SessionKey) (SelectionResult, ProvisionalPin) {
	var skips []SkipDecision
	var pin ProvisionalPin
	attached := false

	for {
		order := c.perm.Perm(3)
		lease, newPin, passSkips, allDisabled, token, didAcquire := c.selectNewSessionOnce(key, order, attached)
		if didAcquire {
			attached = true
			pin = newPin
		}
		skips = append(skips, passSkips...)
		if lease != nil {
			return SelectionResult{Lease: lease, Skips: skips, Outcome: SelectionReserved}, pin
		}
		if allDisabled {
			return SelectionResult{Skips: skips, Outcome: SelectionAllDisabled}, pin
		}

		switch c.Wait(ctx, token) {
		case WaitNotified:
			continue
		case WaitCanceled:
			return SelectionResult{Skips: skips, Outcome: SelectionCanceled}, pin
		case WaitTimedOut:
			return SelectionResult{Skips: skips, Outcome: SelectionCapacityTimeout}, pin
		}
	}
}

// selectNewSessionOnce performs one pass of new-session selection under a
// single lock hold. If a provisional pin exists, it attaches the request
// (once) and reserves the pin's account. If not, it reserves an account and
// installs the pin atomically with the reservation.
func (c *Coordinator) selectNewSessionOnce(key SessionKey, order []int, attached bool) (lease *PendingLease, pin ProvisionalPin, skips []SkipDecision, allDisabled bool, token WaitToken, didAcquire bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A provisional pin already exists: the request must use its account.
	if existing, ok := c.pins[key]; ok && existing.state == PinProvisional {
		if !attached {
			existing.holders++
			c.pins[key] = existing
			didAcquire = true
			pin = ProvisionalPin{
				Account:    existing.account,
				Sequence:   c.nextArrivalLocked(key),
				Generation: existing.generation,
			}
		}
		lease, outcome := c.reserveLocked(existing.account)
		if lease != nil {
			return lease, pin, nil, false, nil, didAcquire
		}
		skips = append(skips, SkipDecision{Account: existing.account, Reason: outcome})
		if outcome == SkippedDisabled {
			// The pinned account is disabled. The provisional pin is dead:
			// remove it and fall through to normal selection.
			delete(c.pins, key)
		} else {
			return nil, pin, skips, false, c.WaitToken(), didAcquire
		}
	}

	// No provisional pin (or the pin was just removed): reserve an account
	// and install the pin atomically.
	var sawRecoverable bool
	for _, idx := range order {
		account := accountFromIndex(idx)
		l, outcome := c.reserveLocked(account)
		if l != nil {
			seq := c.nextArrivalLocked(key)
			c.pins[key] = sessionPin{
				account:     account,
				sequence:    seq,
				nextArrival: seq + 1,
				state:       PinProvisional,
				generation:  1,
				holders:     1,
			}
			pin = ProvisionalPin{Account: account, Sequence: seq, Generation: 1}
			return l, pin, nil, false, nil, true
		}
		skips = append(skips, SkipDecision{Account: account, Reason: outcome})
		if outcome != SkippedDisabled {
			sawRecoverable = true
		}
	}

	if !sawRecoverable {
		return nil, pin, skips, true, nil, false
	}
	return nil, pin, skips, false, c.WaitToken(), false
}

// ReleaseProvisionalHolder releases a request's hold on a provisional pin.
// When the last holder of an unconfirmed generation releases, the pin is
// removed immediately rather than left to expire. A release against a
// confirmed pin, a removed pin, or a different generation is a no-op.
func (c *Coordinator) ReleaseProvisionalHolder(key SessionKey, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if !ok || pin.state != PinProvisional || pin.generation != generation {
		return
	}
	pin.holders--
	if pin.holders <= 0 {
		delete(c.pins, key)
		return
	}
	c.pins[key] = pin
}
