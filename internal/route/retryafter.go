package route

import (
	"strconv"
	"strings"
	"time"
)

// httpDateLayout is the IMF-fixdate format RFC 9110 requires an origin
// server to send for Date and permits for the absolute form of
// Retry-After. HTTP's older obsolete date formats exist only for
// historical compatibility; a modern origin server does not send them,
// and this proxy treats anything else as unparseable rather than carry a
// net/http import for http.ParseTime, which this package's no-I/O static
// check bans wholesale regardless of what it would be used for.
const httpDateLayout = "Mon, 02 Jan 2006 15:04:05 GMT"

// retryAfterDelay is the result of parsing and deriving a Retry-After
// header value.
type retryAfterDelay struct {
	// gate is the duration to advance the account's gate deadline by,
	// before the ten-minute clamp Apply429 applies. It is never negative:
	// a non-positive derived delay becomes one second, the same as an
	// absent, unparseable, or underivable header.
	gate time.Duration

	// stored is what should be recorded as upstream's own statement,
	// unclamped. storedPresent distinguishes "upstream stated a delay"
	// (stored may legitimately be zero, meaning upstream said retry now)
	// from "no delay was derived" (storedPresent is false): the one-second
	// fallback gate is the proxy's own decision in that case, not
	// something upstream said, and the two must not be confused on the
	// stored row.
	stored        time.Duration
	storedPresent bool
}

// parseRetryAfter derives the gate delay and the value to store from a
// Retry-After header value. responseDate is the same response's own Date
// header, already parsed by the caller; hasResponseDate is false when
// that header was absent or unparseable, which makes the absolute form
// underivable regardless of whether header itself parses.
func parseRetryAfter(header string, responseDate time.Time, hasResponseDate bool) retryAfterDelay {
	header = strings.TrimSpace(header)
	if header == "" {
		return retryAfterDelay{gate: time.Second}
	}

	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		return deriveDelay(time.Duration(seconds) * time.Second)
	}

	target, err := time.Parse(httpDateLayout, header)
	if err != nil {
		return retryAfterDelay{gate: time.Second}
	}
	if !hasResponseDate {
		return retryAfterDelay{gate: time.Second}
	}

	return deriveDelay(target.Sub(responseDate))
}

// deriveDelay applies the shared not-positive floor to an already-derived
// delay, whether stated directly as delta-seconds or derived from an
// HTTP-date against the response's own Date.
func deriveDelay(delay time.Duration) retryAfterDelay {
	if delay <= 0 {
		return retryAfterDelay{gate: time.Second, stored: 0, storedPresent: true}
	}
	return retryAfterDelay{gate: delay, stored: delay, storedPresent: true}
}
