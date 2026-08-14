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

func TestRequireScannedEnvelopeRejectsAndRecordsOnce(t *testing.T) {
	writer := &fakeUnroutedRequestWriter{}
	nextCalled := false
	scanned := RequireScannedEnvelope(writer, func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})
	handler := resource.RequireResources(resource.NewGate(), nil, clock.NewRealClock(), RequireBoundedBody(scanned))

	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))

	if nextCalled {
		t.Fatal("next handler ran after scanner rejection")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got, want := recorder.Body.String(), "{\"error\":{\"message\":\"Invalid routing envelope\",\"type\":\"invalid_request_error\",\"code\":\"invalid_request\"}}\n"; got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
	if got, want := writer.recordedCodes(), []proxy.ErrorCode{proxy.ErrInvalidRequest}; !equalErrorCodes(got, want) {
		t.Errorf("recorded codes = %v, want %v", got, want)
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
	handler(recorder, requestWithBody(`{"model":"route"}`))

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
