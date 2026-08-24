package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
	"github.com/gabrielassisxyz/llmux/internal/rewrite"
	"github.com/gabrielassisxyz/llmux/internal/route"
	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// This file holds the full-stack half of the concurrency stress suite:
// requests driven through the real HTTP handler (BuildHandler) into a
// scripted fake upstream, asserting the per-account ceilings at the fake
// upstream's own observations. The coordinator-only half lives in
// internal/route/stress_test.go.

// recordingAdmissionWriter is a fake AdmissionWriter that records every
// admission and can be made to hold the commit, simulating a store that
// delays the admission write close to its ceiling.
type recordingAdmissionWriter struct {
	mu         sync.Mutex
	admissions []store.DispatchAdmission
	gate       chan struct{}
}

func (w *recordingAdmissionWriter) InsertDispatchAdmission(ctx context.Context, a store.DispatchAdmission) error {
	w.mu.Lock()
	gate := w.gate
	w.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	w.mu.Lock()
	w.admissions = append(w.admissions, a)
	w.mu.Unlock()
	return nil
}

func (w *recordingAdmissionWriter) Hold() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gate = make(chan struct{})
}

func (w *recordingAdmissionWriter) Release() {
	w.mu.Lock()
	gate := w.gate
	w.gate = nil
	w.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

func (w *recordingAdmissionWriter) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.admissions)
}

// stressFixture wires the real handler chain to a coordinator, a fake
// admission writer and a scripted fake upstream, all sharing one fake clock.
type stressFixture struct {
	t        *testing.T
	handler  http.Handler
	coord    *route.Coordinator
	clk      *testsupport.FakeClock
	upstream *testsupport.ScriptedUpstream
	writer   *recordingAdmissionWriter
}

func newStressFixture(t *testing.T) *stressFixture {
	t.Helper()
	clk := testsupport.NewFakeClock(time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC))
	clk.AdvanceMonotonic(policy.PostStartDispatchBlackout + time.Second)

	keys := route.AccountKeys{K1: "k1-key", K2: "k2-key", K3: "k3-key"}
	coord := route.NewCoordinator(keys, clk, testsupport.FixedPermutationSource{Values: []int{0, 1, 2}})

	upstream := testsupport.NewScriptedUpstream(clk)
	writer := &recordingAdmissionWriter{}
	gen := idgen.SecureGenerator()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	deps := proxy.DispatchDeps{
		Coordinator:     coord,
		AdmissionWriter: writer,
		Generator:       gen,
		Client:          &http.Client{Transport: upstream},
		Clock:           clk,
		Logger:          logger,
		AccountKeys:     keys,
	}

	gate := resource.NewGateWithClock(clk)
	handler := BuildHandler(HandlerDeps{
		Generator:  gen,
		AuthDigest: testAuthDigest(),
		Gate:       gate,
		Clock:      clk,
	}, proxy.Handlers{
		Models:          proxy.ServeModels,
		ChatCompletions: chatHandler(deps),
	})

	return &stressFixture{t: t, handler: handler, coord: coord, clk: clk, upstream: upstream, writer: writer}
}

// doChat drives one chat-completions request through the handler.
func (f *stressFixture) doChat(model string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.doChatCtx(context.Background(), model)
}

// doChatCtx drives one chat-completions request through the handler with the
// supplied context.
func (f *stressFixture) doChatCtx(ctx context.Context, model string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testBearerKey)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// chatHandler is the minimal chat-completions handler the stress suite
// needs: it replays the scanned body through one dispatch attempt and writes
// the upstream response. The retry loop and relay belong to a later phase;
// this handler exercises the dispatch critical path the ceilings are
// asserted over.
func chatHandler(deps proxy.DispatchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := rewrite.RequestBody(r.Context())
		if !ok {
			proxy.WriteError(w, "", proxy.ErrInvalidRequest)
			return
		}
		metadata, err := rewrite.Scan(body)
		if err != nil {
			proxy.WriteError(w, "", proxy.ErrInvalidRequest)
			return
		}
		entry, ok := catalog.Resolve(metadata.Model)
		if !ok {
			proxy.WriteError(w, "", proxy.ErrModelNotFound)
			return
		}

		reqID, _ := proxy.RequestID(r.Context())
		result := proxy.Dispatch(r.Context(), deps, r, bytes.NewReader(body), int64(len(body)), entry, reqID, 1)
		if result.Response == nil {
			switch {
			case result.Failure != nil && result.Failure.Code != "":
				proxy.WriteError(w, reqID, result.Failure.Code)
			case result.Err != nil:
				var dispatchErr *proxy.DispatchError
				if errors.As(result.Err, &dispatchErr) {
					proxy.WriteError(w, reqID, dispatchErr.Code)
				} else {
					proxy.WriteError(w, reqID, proxy.ErrUpstreamUnavailable)
				}
			default:
				proxy.WriteError(w, reqID, proxy.ErrInternalError)
			}
			return
		}
		defer result.Response.Body.Close()
		defer result.Lease.Release()
		w.WriteHeader(result.Response.StatusCode)
		_, _ = io.Copy(w, result.Response.Body)
	}
}

// waitForCondition polls cond until it returns true or a short deadline
// elapses.
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within the deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestFullStackInFlightAndRateCeilingsAtUpstream drives sixty requests
// pinned to one account through the real handler, holds them at the fake
// upstream, and asserts the upstream itself never observes more than twelve
// live requests for that account's key, then releases and asserts all sixty
// complete and the upstream observed exactly sixty starts.
func TestFullStackInFlightAndRateCeilingsAtUpstream(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		fixture := newStressFixture(t)
		fixture.upstream.HoldRequests()

		const requests = policy.DispatchesPerWindowPerAccount
		var wg sync.WaitGroup
		codes := make(chan int, requests)
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes <- fixture.doChat("kimi-k2.7-k1").Code
			}()
		}

		// The upstream must reach the in-flight ceiling and never exceed it.
		waitForCondition(t, func() bool {
			return fixture.upstream.MaxLive()["k1-key"] >= policy.InFlightAttemptsPerAccount
		})
		if got := fixture.upstream.MaxLive()["k1-key"]; got > policy.InFlightAttemptsPerAccount {
			t.Fatalf("upstream max live = %d, want <= %d", got, policy.InFlightAttemptsPerAccount)
		}

		fixture.upstream.ReleaseRequests()
		wg.Wait()
		close(codes)

		for code := range codes {
			if code != http.StatusOK {
				t.Errorf("status = %d, want 200", code)
			}
		}

		obs := fixture.upstream.Observations()
		if len(obs) != requests {
			t.Fatalf("upstream starts = %d, want %d", len(obs), requests)
		}
		for _, o := range obs {
			if o.Key != "k1-key" {
				t.Errorf("upstream key = %q, want k1-key (attributed by the credential actually sent)", o.Key)
			}
		}
	})
}

// TestFullStackRateSaturatedRequestRejected fills the rolling window with
// sixty dispatches, then proves a sixty-first request is refused with the
// capacity-timeout code once the account-acquisition ceiling elapses.
func TestFullStackRateSaturatedRequestRejected(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		fixture := newStressFixture(t)

		const requests = policy.DispatchesPerWindowPerAccount
		var wg sync.WaitGroup
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if rec := fixture.doChat("kimi-k2.7-k1"); rec.Code != http.StatusOK {
					t.Errorf("status = %d, want 200", rec.Code)
				}
			}()
		}
		wg.Wait()

		if got := len(fixture.upstream.Observations()); got != requests {
			t.Fatalf("upstream starts = %d, want %d", got, requests)
		}

		// The sixty-first request must wait on the full window, then answer
		// 429 once the acquisition ceiling elapses. Fire it in a goroutine so
		// the clock can advance while it waits.
		recCh := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			recCh <- fixture.doChat("kimi-k2.7-k1")
		}()

		// Let the request reach its wait, then advance the clock past the
		// account-acquisition ceiling so its timer fires.
		time.Sleep(50 * time.Millisecond)
		fixture.clk.AdvanceMonotonic(policy.MaxAccountAcquisitionTime + time.Second)

		select {
		case rec := <-recCh:
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("61st status = %d, want 429", rec.Code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("61st request did not complete after the ceiling elapsed")
		}
	})
}

// TestFullStackRateCeilingUnderDelayedAdmissionCommit holds every admission
// commit at the fake store, drives a hundred requests, then releases the
// store and asserts the fake upstream still never observes more than sixty
// starts or twelve live requests for one account. The held commit is the
// case that separates a window anchored at Do from one anchored at
// reservation: a pending reservation must occupy its slot for the whole
// delay.
func TestFullStackRateCeilingUnderDelayedAdmissionCommit(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		fixture := newStressFixture(t)
		fixture.writer.Hold()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const requests = 100
		var wg sync.WaitGroup
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = fixture.doChatCtx(ctx, "kimi-k2.7-k1")
			}()
		}

		// The coordinator admits its in-flight ceiling and holds the rest at
		// the delayed commit; the in-flight count must never exceed twelve.
		waitForCondition(t, func() bool {
			return fixture.coord.InFlight(catalog.AccountK1) == policy.InFlightAttemptsPerAccount
		})
		if got := fixture.coord.InFlight(catalog.AccountK1); got > policy.InFlightAttemptsPerAccount {
			t.Fatalf("coordinator in-flight = %d, want <= %d", got, policy.InFlightAttemptsPerAccount)
		}

		fixture.writer.Release()

		// The admitted requests finalize and dispatch; the rest wait on the
		// full rate window. Cancel the waiters so the test terminates.
		waitForCondition(t, func() bool {
			return len(fixture.upstream.Observations()) >= policy.DispatchesPerWindowPerAccount
		})
		cancel()
		wg.Wait()

		if got := len(fixture.upstream.Observations()); got > policy.DispatchesPerWindowPerAccount {
			t.Fatalf("upstream starts = %d, want <= %d", got, policy.DispatchesPerWindowPerAccount)
		}
		if got := fixture.upstream.MaxLive()["k1-key"]; got > policy.InFlightAttemptsPerAccount {
			t.Fatalf("upstream max live = %d, want <= %d", got, policy.InFlightAttemptsPerAccount)
		}
	})
}

// TestFullStackAliasesSharePerAccountCounts drives requests through two
// different pinned aliases that resolve to the same account and asserts the
// fake upstream attributes every one to that account's single key, so the
// per-account ceiling is shared across aliases rather than per alias.
func TestFullStackAliasesSharePerAccountCounts(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		fixture := newStressFixture(t)

		const perAlias = policy.DispatchesPerWindowPerAccount / 2
		var wg sync.WaitGroup
		for _, model := range []string{"kimi-k2.7-k1", "glm-5.2-k1"} {
			for i := 0; i < perAlias; i++ {
				wg.Add(1)
				go func(model string) {
					defer wg.Done()
					if rec := fixture.doChat(model); rec.Code != http.StatusOK {
						t.Errorf("model %s status = %d, want 200", model, rec.Code)
					}
				}(model)
			}
		}
		wg.Wait()

		obs := fixture.upstream.Observations()
		if len(obs) != policy.DispatchesPerWindowPerAccount {
			t.Fatalf("upstream starts = %d, want %d", len(obs), policy.DispatchesPerWindowPerAccount)
		}
		for _, o := range obs {
			if o.Key != "k1-key" {
				t.Errorf("upstream key = %q, want k1-key (both aliases share the k1 account)", o.Key)
			}
		}
	})
}
