package rewrite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
)

type fakeUnroutedRequestWriter struct {
	mu    sync.Mutex
	codes []proxy.ErrorCode
	err   error
}

func (writer *fakeUnroutedRequestWriter) RecordUnroutedRequest(_ context.Context, code proxy.ErrorCode) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.codes = append(writer.codes, code)
	return writer.err
}

func (writer *fakeUnroutedRequestWriter) recordedCodes() []proxy.ErrorCode {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]proxy.ErrorCode(nil), writer.codes...)
}

func TestRequireScannedEnvelopeMapsEveryOwnedRejection(t *testing.T) {
	overDepth := `{"model":"kimi-k2.7","x":` + strings.Repeat("[", policy.MaxJSONNestingDepth) + `1` + strings.Repeat("]", policy.MaxJSONNestingDepth) + `}`
	cases := []struct {
		name string
		body string
		code proxy.ErrorCode
	}{
		{name: "invalid JSON", body: `{"model":"kimi-k2.7"`, code: proxy.ErrInvalidRequest},
		{name: "non-object", body: `[]`, code: proxy.ErrInvalidRequest},
		{name: "missing model", body: `{}`, code: proxy.ErrInvalidRequest},
		{name: "non-string model", body: `{"model":1}`, code: proxy.ErrInvalidRequest},
		{name: "duplicate model", body: `{"model":"kimi-k2.7","model":"kimi-k2.7"}`, code: proxy.ErrInvalidRequest},
		{name: "duplicate stream", body: `{"model":"kimi-k2.7","stream":true,"stream":false}`, code: proxy.ErrInvalidRequest},
		{name: "depth exceeded", body: overDepth, code: proxy.ErrJSONDepthExceeded},
		{name: "unknown alias", body: `{"model":"unknown"}`, code: proxy.ErrModelNotFound},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			writer := &fakeUnroutedRequestWriter{}
			nextCalled := false
			handler := RequireScannedEnvelope(writer, func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			})

			recorder := httptest.NewRecorder()
			handler(recorder, requestWithBody(test.body))

			if nextCalled {
				t.Fatal("next handler ran after scanner rejection")
			}
			assertErrorResponse(t, recorder, test.code)
			if got, want := writer.recordedCodes(), []proxy.ErrorCode{test.code}; !equalErrorCodes(got, want) {
				t.Errorf("recorded codes = %v, want %v", got, want)
			}
		})
	}
}

func TestRequireScannedEnvelopeAcceptsWithoutRecording(t *testing.T) {
	writer := &fakeUnroutedRequestWriter{}
	nextCalled := false
	handler := RequireScannedEnvelope(writer, func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	handler(recorder, requestWithBody(`{"model":"kimi-k2.7"}`))

	if !nextCalled {
		t.Fatal("next handler did not run for a valid envelope")
	}
	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := writer.recordedCodes(); len(got) != 0 {
		t.Errorf("recorded codes = %v, want none", got)
	}
}

func TestRequireScannedEnvelopeReadsBoundedBodyContext(t *testing.T) {
	writer := &fakeUnroutedRequestWriter{}
	nextCalled := false
	scanned := RequireScannedEnvelope(writer, func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})
	handler := resource.RequireResources(resource.NewGate(), nil, clock.NewRealClock(), RequireBoundedBody(writer, scanned))

	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"unknown"}`)))

	if nextCalled {
		t.Fatal("next handler ran after unknown alias")
	}
	assertErrorResponse(t, recorder, proxy.ErrModelNotFound)
	if got, want := writer.recordedCodes(), []proxy.ErrorCode{proxy.ErrModelNotFound}; !equalErrorCodes(got, want) {
		t.Errorf("recorded codes = %v, want %v", got, want)
	}
}

func TestRequireScannedEnvelopePassesOpaqueParametersAndDuplicateUnknownMembers(t *testing.T) {
	writer := &fakeUnroutedRequestWriter{}
	body := `{"model":"kimi-k2.7","unrecognized":1,"unrecognized":{"nested":[true,false]},"vendor_field":"value"}`
	var gotBody []byte
	handler := RequireScannedEnvelope(writer, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = RequestBody(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	handler(recorder, requestWithBody(body))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := string(gotBody); got != body {
		t.Errorf("body = %q, want byte-identical %q", got, body)
	}
	if got := writer.recordedCodes(); len(got) != 0 {
		t.Errorf("recorded codes = %v, want none", got)
	}
}

func TestRequireScannedEnvelopeWriterFailureDoesNotChangeResponse(t *testing.T) {
	validWriter := &fakeUnroutedRequestWriter{}
	failingWriter := &fakeUnroutedRequestWriter{err: errors.New("store unavailable")}

	validResponse := serveEnvelopeRejection(validWriter)
	failingResponse := serveEnvelopeRejection(failingWriter)

	if failingResponse != validResponse {
		t.Errorf("writer failure response = %q, want byte-identical %q", failingResponse, validResponse)
	}
	if got := failingWriter.recordedCodes(); len(got) != 1 {
		t.Errorf("failing writer calls = %d, want 1", len(got))
	}
}

func TestRequireScannedEnvelopeConcurrentRejectionsRecordIndividually(t *testing.T) {
	writer := &fakeUnroutedRequestWriter{}
	handler := RequireScannedEnvelope(writer, func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler ran after scanner rejection")
	})

	const requests = 32
	var group sync.WaitGroup
	group.Add(requests)
	for range requests {
		go func() {
			defer group.Done()
			handler(httptest.NewRecorder(), requestWithBody(`{}`))
		}()
	}
	group.Wait()

	if got := writer.recordedCodes(); len(got) != requests {
		t.Errorf("recorded codes = %d, want %d", len(got), requests)
	}
}

func requestWithBody(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return request.WithContext(context.WithValue(request.Context(), requestBodyKey, []byte(body)))
}

func serveEnvelopeRejection(writer UnroutedRequestWriter) string {
	recorder := httptest.NewRecorder()
	RequireScannedEnvelope(writer, func(http.ResponseWriter, *http.Request) {})(recorder, requestWithBody(`{}`))
	response := recorder.Result()
	return fmt.Sprintf("%d\n%s\n%s", response.StatusCode, response.Header.Get("Content-Type"), recorder.Body.String())
}

func equalErrorCodes(got, want []proxy.ErrorCode) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, code proxy.ErrorCode) {
	t.Helper()
	wantStatus := http.StatusBadRequest
	if code == proxy.ErrModelNotFound {
		wantStatus = http.StatusNotFound
	}
	if recorder.Code != wantStatus {
		t.Errorf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"`+string(code)+`"`) {
		t.Errorf("response = %q, want code %q", recorder.Body.String(), code)
	}
}
