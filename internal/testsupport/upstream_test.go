package testsupport

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestScriptedUpstreamAttributesByBearerKey proves the fake records the
// credential it actually received, not the account a test intended: two
// requests carrying different bearer keys must land in different buckets
// even though the test knows both are "the same account" in some other
// sense.
func TestScriptedUpstreamAttributesByBearerKey(t *testing.T) {
	clk := NewFakeClock(time.Unix(0, 0))
	up := NewScriptedUpstream(clk)

	client := &http.Client{Transport: up}
	for _, key := range []string{"k1-key", "k2-key"} {
		req, err := http.NewRequest(http.MethodPost, "https://ollama.com/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()
	}

	obs := up.Observations()
	if len(obs) != 2 {
		t.Fatalf("observations = %d, want 2", len(obs))
	}
	if obs[0].Key != "k1-key" || obs[1].Key != "k2-key" {
		t.Fatalf("keys = %q, %q; want k1-key, k2-key", obs[0].Key, obs[1].Key)
	}
	if string(obs[0].Body) != `{"model":"x"}` {
		t.Errorf("body = %q, want the request bytes", obs[0].Body)
	}
	if obs[0].Start != 0 {
		t.Errorf("start = %v, want the injected clock's zero instant", obs[0].Start)
	}
}

// TestScriptedUpstreamTracksLiveConcurrency holds requests at the fake and
// asserts the peak live count per key is recorded, then that releasing the
// hold drains the live count back to zero.
func TestScriptedUpstreamTracksLiveConcurrency(t *testing.T) {
	clk := NewFakeClock(time.Unix(0, 0))
	up := NewScriptedUpstream(clk)
	up.HoldRequests()

	client := &http.Client{Transport: up}
	const requests = 3
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, "https://ollama.com/v1/chat/completions", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer k1-key")
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}

	waitFor(t, func() bool { return up.Live()["k1-key"] == requests })

	if got := up.MaxLive()["k1-key"]; got != requests {
		t.Fatalf("max live = %d, want %d", got, requests)
	}

	up.ReleaseRequests()
	wg.Wait()

	if got := up.Live()["k1-key"]; got != 0 {
		t.Errorf("live after release = %d, want 0", got)
	}
}

// TestScriptedUpstreamRecordsCancellation holds a request and cancels its
// context, asserting the fake unblocks on cancellation and records it.
func TestScriptedUpstreamRecordsCancellation(t *testing.T) {
	clk := NewFakeClock(time.Unix(0, 0))
	up := NewScriptedUpstream(clk)
	up.HoldRequests()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://ollama.com/v1/chat/completions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("Authorization", "Bearer k1-key")

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := (&http.Client{Transport: up}).Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitFor(t, func() bool { return up.Live()["k1-key"] == 1 })
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("held request did not unblock on cancellation")
	}

	obs := up.Observations()
	if len(obs) != 1 || !obs[0].Canceled {
		t.Fatalf("observation = %+v, want one canceled observation", obs)
	}
}

// waitFor polls cond until it returns true or a short deadline elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within the deadline")
		}
		time.Sleep(time.Millisecond)
	}
}
