package errcode

import (
	"math"
	"time"
)

// CalculateRetryAfter returns the Retry-After header value in whole seconds,
// rounded up, for the 429 rate-limit envelope. It is never less than 1.
// It lives here rather than in internal/proxy so internal/route can name
// the same computation without importing the HTTP package.
func CalculateRetryAfter(reopen time.Time, now time.Time) int {
	if reopen.IsZero() || reopen.Before(now) {
		return 1
	}
	diff := reopen.Sub(now).Seconds()
	sec := int(math.Ceil(diff))
	if sec < 1 {
		return 1
	}
	return sec
}
