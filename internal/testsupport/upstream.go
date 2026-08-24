package testsupport

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/clock"
)

// UpstreamObservation is one dispatch the scripted upstream received. Key is
// the bearer credential actually present in the Authorization header, never
// the account a test intended to send, so a test that attributes by intent
// rather than by the credential sent would hide exactly the class of defect
// the stress suite exists to find.
type UpstreamObservation struct {
	// Key is the bearer credential extracted from the Authorization
	// header the upstream actually received.
	Key string
	// Start is the monotonic instant the dispatch began, read from the
	// injected clock at the moment RoundTrip was entered.
	Start time.Duration
	// Body is the request bytes the upstream received.
	Body []byte
	// Canceled reports whether the request context had ended by the time
	// the upstream finished observing the request.
	Canceled bool
}

// ScriptedUpstream is a fake upstream that records every dispatch it
// receives, attributed to an account by the bearer key actually sent. It
// implements http.RoundTripper so it can be installed as an http.Client's
// transport, and it records live concurrency per key so a test can assert
// the per-account in-flight ceiling at the observation point that matters.
type ScriptedUpstream struct {
	mu  sync.Mutex
	clk clock.Clock

	observations []UpstreamObservation
	live         map[string]int
	maxLive      map[string]int

	// respond builds the response for a request. When nil, a 200 with an
	// empty body is returned.
	respond func(req *http.Request) *http.Response

	// gate, when non-nil, blocks every request until it is closed, so a
	// test can hold requests in flight and observe live concurrency at its
	// peak.
	gate chan struct{}
}

// NewScriptedUpstream constructs a fake upstream reading monotonic time from
// clk.
func NewScriptedUpstream(clk clock.Clock) *ScriptedUpstream {
	return &ScriptedUpstream{
		clk:     clk,
		live:    make(map[string]int),
		maxLive: make(map[string]int),
	}
}

// SetRespond installs the response builder used for every request. Pass nil
// to restore the default 200 with an empty body.
func (u *ScriptedUpstream) SetRespond(respond func(req *http.Request) *http.Response) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.respond = respond
}

// HoldRequests makes every subsequent request block until ReleaseRequests is
// called. It is idempotent: a second call while already holding replaces the
// gate, so requests admitted after the first hold block on the new gate.
func (u *ScriptedUpstream) HoldRequests() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.gate = make(chan struct{})
}

// ReleaseRequests unblocks every held request. It is a no-op when no hold is
// active.
func (u *ScriptedUpstream) ReleaseRequests() {
	u.mu.Lock()
	gate := u.gate
	u.gate = nil
	u.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

// RoundTrip records one dispatch and answers it. It never returns an error:
// the fake upstream is a scripted success path, and transport failures are
// exercised by other fakes.
func (u *ScriptedUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	key := bearerKey(req)
	start := u.clk.MonotonicNow()
	body, _ := io.ReadAll(req.Body)

	u.mu.Lock()
	u.live[key]++
	if u.live[key] > u.maxLive[key] {
		u.maxLive[key] = u.live[key]
	}
	u.observations = append(u.observations, UpstreamObservation{
		Key:   key,
		Start: start,
		Body:  body,
	})
	idx := len(u.observations) - 1
	gate := u.gate
	u.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-req.Context().Done():
		}
	}

	// Record cancellation after the hold, so a request canceled while held
	// is observed as canceled rather than as the healthy request it was at
	// entry.
	u.mu.Lock()
	u.observations[idx].Canceled = req.Context().Err() != nil
	u.mu.Unlock()

	resp := u.response(req)

	u.mu.Lock()
	u.live[key]--
	u.mu.Unlock()

	return resp, nil
}

func (u *ScriptedUpstream) response(req *http.Request) *http.Response {
	u.mu.Lock()
	respond := u.respond
	u.mu.Unlock()
	if respond != nil {
		return respond(req)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
}

// Observations returns a snapshot of every dispatch received, in arrival
// order.
func (u *ScriptedUpstream) Observations() []UpstreamObservation {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]UpstreamObservation(nil), u.observations...)
}

// MaxLive returns the peak concurrent in-flight count per bearer key.
func (u *ScriptedUpstream) MaxLive() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]int, len(u.maxLive))
	for k, v := range u.maxLive {
		out[k] = v
	}
	return out
}

// Live returns the current in-flight count per bearer key.
func (u *ScriptedUpstream) Live() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]int, len(u.live))
	for k, v := range u.live {
		out[k] = v
	}
	return out
}

// bearerKey extracts the bearer credential from the Authorization header. It
// returns the empty string when the header is absent or not a bearer token.
func bearerKey(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}
