package route

import (
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// RecoveredPin is one session's recovered affinity, in route terms: the
// account that served the request that arrived last, and the wall-clock
// instant that request finished, which anchors the recovered pin's expiry.
type RecoveredPin struct {
	Key        SessionKey
	Account    catalog.Account
	FinishedAt time.Time
}

// RecoverPins installs recovered confirmed pins into the coordinator. Each
// pin expires at FinishedAt + SessionAffinityTTL, matching a live pin
// established at that instant, so a recovered pin and a live one expire at
// the same wall instant. A FinishedAt in the future is clamped to the current
// wall time and counted in the returned clamped count, so the caller can warn.
//
// It recovers nothing else: rate state, in-flight counts and disabled health
// are left at their fresh-process values, and no upstream request is made.
// The post-start blackout already holds every account closed for one full
// rolling window, so no rate state needs to be recovered.
func (c *Coordinator) RecoverPins(pins []RecoveredPin) (clamped int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clk.WallNow()
	for _, pin := range pins {
		finishedAt := pin.FinishedAt
		if finishedAt.After(now) {
			finishedAt = now
			clamped++
		}
		// A recovered pin carries no live arrival sequence: sequence 0 lets
		// the next live confirmation (sequence 1) win, and nextArrival 1 is
		// the sequence the next request for this session receives.
		c.pins[pin.Key] = sessionPin{
			account:     pin.Account,
			expiry:      finishedAt.Add(policy.SessionAffinityTTL),
			sequence:    0,
			nextArrival: 1,
			state:       PinConfirmed,
		}
	}
	return clamped
}
