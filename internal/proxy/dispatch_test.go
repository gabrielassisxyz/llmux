package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/route"
	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// recordingAdmissionWriter records the admission sequence and can be made
// to fail, simulating a store that cannot commit.
type recordingAdmissionWriter struct {
	log        *[]string
	err        error
	admissions []store.DispatchAdmission
}

func (w *recordingAdmissionWriter) InsertDispatchAdmission(ctx context.Context, a store.DispatchAdmission) error {
	*w.log = append(*w.log, "admission")
	if w.err != nil {
		return w.err
	}
	w.admissions = append(w.admissions, a)
	return nil
}

// recordingRoundTripper records the Do sequence and the request it saw, and
// can be made to fail, simulating a transport error.
type recordingRoundTripper struct {
	log  *[]string
	req  *http.Request
	resp *http.Response
	err  error
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	*rt.log = append(*rt.log, "do")
	rt.req = req
	if rt.err != nil {
		return nil, rt.err
	}
	return rt.resp, nil
}

func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// newDispatchDeps assembles a dispatch dependency set with a fake clock
// advanced past the post-start blackout and a deterministic permutation.
func newDispatchDeps(t *testing.T, writer store.AdmissionWriter, rt http.RoundTripper) (DispatchDeps, *route.Coordinator) {
	t.Helper()
	clk := testsupport.NewFakeClock(time.Unix(0, 0))
	clk.AdvanceMonotonic(61 * time.Second)
	coord := route.NewCoordinator(
		route.AccountKeys{K1: "k1-key", K2: "k2-key", K3: "k3-key"},
		clk,
		testsupport.FixedPermutationSource{Values: []int{0, 1, 2}},
	)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return DispatchDeps{
		Coordinator:     coord,
		AdmissionWriter: writer,
		Generator:       idgen.NewGenerator(bytes.NewReader(make([]byte, 16))),
		Client:          &http.Client{Transport: rt},
		Clock:           clk,
		Logger:          logger,
		AccountKeys:     route.AccountKeys{K1: "k1-key", K2: "k2-key", K3: "k3-key"},
	}, coord
}

func baseEntry(t *testing.T) catalog.Route {
	t.Helper()
	entry, ok := catalog.Resolve("kimi-k2.7")
	if !ok {
		t.Fatal("resolve kimi-k2.7")
	}
	return entry
}

func TestDispatchCommitsAdmissionBeforeDo(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log}
	rt := &recordingRoundTripper{log: &log, resp: okResponse()}
	deps, _ := newDispatchDeps(t, writer, rt)

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7"}`)), 20, baseEntry(t), "logical-1", 1)

	if result.Response == nil || result.Lease == nil {
		t.Fatalf("dispatch did not succeed: %+v", result)
	}
	if result.Failure != nil || result.Err != nil {
		t.Fatalf("unexpected failure: %+v", result)
	}
	if len(log) != 2 || log[0] != "admission" || log[1] != "do" {
		t.Fatalf("sequence = %v, want [admission do]", log)
	}
	if len(writer.admissions) != 1 {
		t.Fatalf("admission rows = %d, want 1", len(writer.admissions))
	}
	admission := writer.admissions[0]
	if admission.LogicalRequestID != "logical-1" || admission.AttemptNo != 1 {
		t.Errorf("admission identity = %q/%d, want logical-1/1", admission.LogicalRequestID, admission.AttemptNo)
	}
	if admission.AccountLabel != string(catalog.AccountK1) {
		t.Errorf("admission account = %q, want k1", admission.AccountLabel)
	}
	if admission.RequestedAlias != "kimi-k2.7" || admission.UpstreamModel != "kimi-k2.7-code:cloud" {
		t.Errorf("admission route = %q/%q", admission.RequestedAlias, admission.UpstreamModel)
	}
}

func TestDispatchFailedAdmissionFreesSlotsAndReturns503(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log, err: errors.New("disk full")}
	rt := &recordingRoundTripper{log: &log, resp: okResponse()}
	deps, coord := newDispatchDeps(t, writer, rt)

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7"}`)), 20, baseEntry(t), "logical-1", 1)

	if result.Response != nil || result.Lease != nil {
		t.Fatalf("dispatch unexpectedly succeeded: %+v", result)
	}
	var dispatchErr *DispatchError
	if !errors.As(result.Err, &dispatchErr) || dispatchErr.Code != ErrAdmissionStoreUnavailable {
		t.Fatalf("Err = %v, want DispatchError{Code: admission_store_unavailable}", result.Err)
	}
	if len(log) != 1 || log[0] != "admission" {
		t.Fatalf("sequence = %v, want [admission] (no Do)", log)
	}
	if got := coord.InFlight(catalog.AccountK1); got != 0 {
		t.Errorf("in-flight after failed admission = %d, want 0 (lease released)", got)
	}
}

func TestDispatchSelectionFailureClassifies(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log}
	rt := &recordingRoundTripper{log: &log, resp: okResponse()}
	deps, coord := newDispatchDeps(t, writer, rt)
	coord.Disable(catalog.AccountK1)
	coord.Disable(catalog.AccountK2)
	coord.Disable(catalog.AccountK3)

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7"}`)), 20, baseEntry(t), "logical-1", 1)

	if result.Response != nil || result.Lease != nil {
		t.Fatalf("dispatch unexpectedly succeeded: %+v", result)
	}
	if result.Failure == nil {
		t.Fatal("Failure = nil, want a classified selection failure")
	}
	if result.Failure.Code != ErrAccountUnavailable {
		t.Errorf("Failure.Code = %q, want %q", result.Failure.Code, ErrAccountUnavailable)
	}
	if len(log) != 0 {
		t.Fatalf("sequence = %v, want empty (no admission, no Do)", log)
	}
}

func TestDispatchDoFailureReleasesLease(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log}
	transportErr := errors.New("connection reset")
	rt := &recordingRoundTripper{log: &log, err: transportErr}
	deps, coord := newDispatchDeps(t, writer, rt)

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7"}`)), 20, baseEntry(t), "logical-1", 1)

	if result.Response != nil || result.Lease != nil {
		t.Fatalf("dispatch unexpectedly succeeded: %+v", result)
	}
	if !errors.Is(result.Err, transportErr) {
		t.Fatalf("Err = %v, want the transport error", result.Err)
	}
	if len(log) != 2 || log[0] != "admission" || log[1] != "do" {
		t.Fatalf("sequence = %v, want [admission do]", log)
	}
	if got := coord.InFlight(catalog.AccountK1); got != 0 {
		t.Errorf("in-flight after Do failure = %d, want 0 (lease released)", got)
	}
}

func TestDispatchSetsAccountAuthorization(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log}
	rt := &recordingRoundTripper{log: &log, resp: okResponse()}
	deps, _ := newDispatchDeps(t, writer, rt)

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7"}`)), 20, baseEntry(t), "logical-1", 1)

	if result.Response == nil {
		t.Fatalf("dispatch did not succeed: %+v", result)
	}
	if got := rt.req.Header.Get("Authorization"); got != "Bearer k1-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer k1-key")
	}
}

func TestDispatchPinnedAliasUsesExplicitSelection(t *testing.T) {
	var log []string
	writer := &recordingAdmissionWriter{log: &log}
	rt := &recordingRoundTripper{log: &log, resp: okResponse()}
	deps, _ := newDispatchDeps(t, writer, rt)

	pinned, ok := catalog.Resolve("kimi-k2.7-k2")
	if !ok {
		t.Fatal("resolve kimi-k2.7-k2")
	}

	clientReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7-k2"}`))
	result := Dispatch(context.Background(), deps, clientReq, io.MultiReader(strings.NewReader(`{"model":"kimi-k2.7-k2"}`)), 22, pinned, "logical-1", 1)

	if result.Response == nil {
		t.Fatalf("dispatch did not succeed: %+v", result)
	}
	if got := rt.req.Header.Get("Authorization"); got != "Bearer k2-key" {
		t.Errorf("Authorization = %q, want %q (pinned k2)", got, "Bearer k2-key")
	}
	if len(writer.admissions) != 1 || writer.admissions[0].AccountLabel != string(catalog.AccountK2) {
		t.Errorf("admission account = %v, want k2", writer.admissions)
	}
}
