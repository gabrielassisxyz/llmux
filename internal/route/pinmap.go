package route

import (
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// sessionSweepInterval is how many session operations pass between
// foreground sweeps of the pin map. It is stated in PLAN.md §16.2 rather
// than in the policy table, so it is defined here where §16.2 is
// implemented.
const sessionSweepInterval = 256

// noteSessionOpLocked counts one session operation and runs the foreground
// sweep every sessionSweepInterval operations. The sweep removes expired
// confirmed pins; provisional pins have no wall-clock expiry and are
// removed by holder release instead. The caller must hold c.mu.
func (c *Coordinator) noteSessionOpLocked() {
	c.sessionOps++
	if c.sessionOps < sessionSweepInterval {
		return
	}
	c.sessionOps = 0

	now := c.clk.WallNow()
	for key, pin := range c.pins {
		if pin.state == PinConfirmed && !now.Before(pin.expiry) {
			delete(c.pins, key)
		}
	}
}

// pinMapFullLocked reports whether the pin map is at its ceiling. The
// caller must hold c.mu.
func (c *Coordinator) pinMapFullLocked() bool {
	return len(c.pins) >= policy.LiveSessionPins
}
