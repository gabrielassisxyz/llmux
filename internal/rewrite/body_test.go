package rewrite

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
)

// newRequestResources obtains a working *resource.RequestResources backed by
// g, by composing resource.RequireResources exactly as production wiring
// would and capturing the value it attaches to the request context. The
// captured value remains usable for further Acquire/Release calls after
// this returns.
func newRequestResources(t *testing.T, g *resource.Gate) *resource.RequestResources {
	t.Helper()
	var captured *resource.RequestResources
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), func(_ http.ResponseWriter, r *http.Request) {
		captured = resource.ContextResources(r.Context())
	})
	wrapped(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if captured == nil {
		t.Fatal("expected RequestResources in context")
	}
	return captured
}

// countingReader yields exactly remaining bytes of filler content, then EOF.
type countingReader struct {
	remaining int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.remaining {
		n = c.remaining
	}
	for i := range p[:n] {
		p[i] = 'x'
	}
	c.remaining -= n
	return n, nil
}

type erroringReader struct {
	err error
}

func (e *erroringReader) Read(_ []byte) (int, error) {
	return 0, e.err
}

// vanishingReader simulates a client that disconnects exactly during the
// body read: cancel fires synchronously before the read error is returned,
// so the caller observes both together, as a real dropped connection would.
type vanishingReader struct {
	cancel context.CancelFunc
	err    error
}

func (v *vanishingReader) Read(_ []byte) (int, error) {
	v.cancel()
	return 0, v.err
}

func unsizedRequest(n int) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = -1
	r.Body = io.NopCloser(&countingReader{remaining: n})
	return r
}

func TestReadKnownLengthBody_ExactBoundaryAccepted(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	data := make([]byte, policy.MaxRequestBodyBytes)
	r := httptest.NewRequest(http.MethodPost, "/", &byteReader{data: data})
	r.ContentLength = int64(len(data))

	body, err := readBoundedBody(r, res)
	if err != nil {
		t.Fatalf("expected the exact 64 MiB boundary to be accepted, got %v", err)
	}
	if len(body) != policy.MaxRequestBodyBytes {
		t.Errorf("expected body length %d, got %d", policy.MaxRequestBodyBytes, len(body))
	}
}

func TestReadKnownLengthBody_OverBoundaryRejected(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = int64(policy.MaxRequestBodyBytes) + 1

	_, err := readBoundedBody(r, res)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("expected errBodyTooLarge for one byte over the boundary, got %v", err)
	}

	// A declared-oversize body must never be charged: the gate should still
	// hold its entire budget.
	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("expected the whole budget untouched by a rejected declared length, got %v", err)
	}
}

func TestReadUnsizedBody_ExactBoundaryAccepted(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	r := unsizedRequest(policy.MaxRequestBodyBytes)

	body, err := readBoundedBody(r, res)
	if err != nil {
		t.Fatalf("expected the exact 64 MiB unsized boundary to be accepted, got %v", err)
	}
	if len(body) != policy.MaxRequestBodyBytes {
		t.Errorf("expected body length %d, got %d", policy.MaxRequestBodyBytes, len(body))
	}
	if cap(body) != policy.MaxRequestBodyBytes {
		t.Errorf("expected final capacity exactly %d, got %d", policy.MaxRequestBodyBytes, cap(body))
	}
}

func TestReadUnsizedBody_OverBoundaryRejected(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	r := unsizedRequest(policy.MaxRequestBodyBytes + 1)

	_, err := readBoundedBody(r, res)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("expected errBodyTooLarge for a body one byte over the unsized boundary, got %v", err)
	}

	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("expected the whole budget recoverable after an oversized unsized body, got %v", err)
	}
}

func TestReadUnsizedBody_ChargeTracksFinalCapacityNotLength(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	// 3.5 steps of data forces growth past the initial step and settles at
	// less than the final doubled capacity.
	step := policy.UnknownLengthBodyChargeStepBytes
	n := 3*step + step/2
	r := unsizedRequest(n)

	body, err := readBoundedBody(r, res)
	if err != nil {
		t.Fatalf("failed to read unsized body: %v", err)
	}
	if len(body) != n {
		t.Errorf("expected body length %d, got %d", n, len(body))
	}
	// Growth doubles 1 -> 2 -> 4 steps to cover 3.5 steps of data.
	wantCap := 4 * step
	if cap(body) != wantCap {
		t.Errorf("expected final capacity %d (next power-of-two step), got %d", wantCap, cap(body))
	}

	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes - wantCap); err != nil {
		t.Fatalf("expected remaining budget to reflect capacity %d, got error %v", wantCap, err)
	}
}

func TestReadUnsizedBody_ContextCancelledDuringGrowthReleasesCharge(t *testing.T) {
	g := resource.NewGate()
	res := newRequestResources(t, g)

	step := policy.UnknownLengthBodyChargeStepBytes
	r := unsizedRequest(step + 1) // fills the initial step exactly, forcing a growth step

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = r.WithContext(ctx)

	_, err := readBoundedBody(r, res)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("expected the whole budget recoverable after a cancelled growth, got %v", err)
	}
}

func TestRequireBoundedBody_OversizeIsLocal413(t *testing.T) {
	g := resource.NewGate()
	writer := &fakeUnroutedRequestWriter{}
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), RequireBoundedBody(writer, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run for an oversized body")
	}))

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = int64(policy.MaxRequestBodyBytes) + 1
	w := httptest.NewRecorder()

	wrapped(w, r)

	if w.Code != 413 {
		t.Errorf("status = %d, want 413", w.Code)
	}
	if got, want := writer.recordedCodes(), []proxy.ErrorCode{proxy.ErrRequestTooLarge}; !equalErrorCodes(got, want) {
		t.Errorf("recorded codes = %v, want %v", got, want)
	}
}

func TestRequireBoundedBody_ReadErrorIsLocal400(t *testing.T) {
	g := resource.NewGate()
	writer := &fakeUnroutedRequestWriter{}
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), RequireBoundedBody(writer, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run when the body fails to read")
	}))

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = 10
	r.Body = io.NopCloser(&erroringReader{err: errors.New("connection reset")})
	w := httptest.NewRecorder()

	wrapped(w, r)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if got, want := writer.recordedCodes(), []proxy.ErrorCode{proxy.ErrInvalidRequest}; !equalErrorCodes(got, want) {
		t.Errorf("recorded codes = %v, want %v", got, want)
	}
}

func TestRequireBoundedBody_ClientVanishedWritesNothing(t *testing.T) {
	g := resource.NewGate()
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), RequireBoundedBody(&fakeUnroutedRequestWriter{}, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run when the body fails to read")
	}))

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = 10
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	r.Body = io.NopCloser(&vanishingReader{cancel: cancel, err: errors.New("connection reset")})

	w := httptest.NewRecorder()
	wrapped(w, r)

	if w.Code != 200 {
		t.Errorf("expected nothing written (default 200), got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected an empty response body, got %q", w.Body.String())
	}
}

func TestRequireBoundedBody_SuccessAttachesBodyToContext(t *testing.T) {
	g := resource.NewGate()
	const want = "hello world"

	var gotBody []byte
	var gotOK bool
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), RequireBoundedBody(&fakeUnroutedRequestWriter{}, func(w http.ResponseWriter, r *http.Request) {
		gotBody, gotOK = RequestBody(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/", &byteReader{data: []byte(want)})
	r.ContentLength = int64(len(want))
	w := httptest.NewRecorder()

	wrapped(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !gotOK {
		t.Fatal("expected the request body to be attached to the context")
	}
	if string(gotBody) != want {
		t.Errorf("expected body %q, got %q", want, gotBody)
	}

	// The gate must be fully recovered after the handler chain unwinds.
	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("expected the whole budget recoverable after a successful request, got %v", err)
	}
}

func TestRequireBoundedBody_GateDenialIsLocal429(t *testing.T) {
	g := resource.NewGate()
	if err := g.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("failed to exhaust the gate: %v", err)
	}

	writer := &fakeUnroutedRequestWriter{}
	wrapped := resource.RequireResources(g, nil, clock.NewRealClock(), RequireBoundedBody(writer, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run when the memory gate denies the charge")
	}))

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.ContentLength = 10
	w := httptest.NewRecorder()

	wrapped(w, r)

	if w.Code != 429 {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if got, want := writer.recordedCodes(), []proxy.ErrorCode{proxy.ErrProxyOverloaded}; !equalErrorCodes(got, want) {
		t.Errorf("recorded codes = %v, want %v", got, want)
	}
	if w.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After: 1, got %q", w.Header().Get("Retry-After"))
	}
}

// byteReader is a minimal io.Reader over a byte slice, used instead of
// bytes.Reader so httptest.NewRequest does not special-case it and infer
// r.ContentLength on its own; tests set ContentLength explicitly.
type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}
