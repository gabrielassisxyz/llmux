package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestSessionKeyFromRequestUsesVersionedStableDigest(t *testing.T) {
	request := sessionRequest("session-marker")
	first, err := SessionKeyFromRequest(request, []byte("affinity-key"))
	if err != nil {
		t.Fatalf("SessionKeyFromRequest() error = %v", err)
	}
	second, err := SessionKeyFromRequest(sessionRequest("session-marker"), []byte("affinity-key"))
	if err != nil {
		t.Fatalf("SessionKeyFromRequest() error = %v", err)
	}
	if first == nil || second == nil {
		t.Fatal("SessionKeyFromRequest() returned nil key")
	}
	if *first != *second {
		t.Fatal("same header and key produced different session keys")
	}
	if first[0] != sessionKeyVersion {
		t.Fatalf("session key version = %d, want %d", first[0], sessionKeyVersion)
	}
	if string(first[:]) == "session-marker" || strings.Contains(string(first[:]), "session-marker") {
		t.Fatal("session key retained the raw session identifier")
	}
}

func TestSessionKeyFromRequestReturnsNilForMissingOrEmptyHeader(t *testing.T) {
	for _, request := range []*http.Request{sessionRequest(), sessionRequest("")} {
		key, err := SessionKeyFromRequest(request, []byte("affinity-key"))
		if err != nil {
			t.Fatalf("SessionKeyFromRequest() error = %v", err)
		}
		if key != nil {
			t.Fatalf("SessionKeyFromRequest() key = %v, want nil", key)
		}
	}
}

func TestSessionKeyFromRequestRejectsMultipleOrOversizedHeaders(t *testing.T) {
	multiple := sessionRequest("first")
	multiple.Header.Add("X-Session-ID", "second")
	overlong := sessionRequest(strings.Repeat("a", sessionHeaderMaxBytes+1))

	for _, request := range []*http.Request{multiple, overlong} {
		key, err := SessionKeyFromRequest(request, []byte("affinity-key"))
		if err == nil {
			t.Fatal("SessionKeyFromRequest() error = nil, want invalid session header")
		}
		if key != nil {
			t.Fatalf("SessionKeyFromRequest() key = %v, want nil", key)
		}
	}
}

func TestSessionKeyFromRequestIsCaseSensitiveAndKeyed(t *testing.T) {
	lower, err := SessionKeyFromRequest(sessionRequest("session"), []byte("affinity-key"))
	if err != nil {
		t.Fatal(err)
	}
	upper, err := SessionKeyFromRequest(sessionRequest("Session"), []byte("affinity-key"))
	if err != nil {
		t.Fatal(err)
	}
	differentKey, err := SessionKeyFromRequest(sessionRequest("session"), []byte("different-key"))
	if err != nil {
		t.Fatal(err)
	}
	if *lower == *upper || *lower == *differentKey {
		t.Fatal("session digest ignored identifier case or HMAC key")
	}
}

func sessionRequest(values ...string) *http.Request {
	request := &http.Request{Header: make(http.Header)}
	for _, value := range values {
		request.Header.Add("X-Session-ID", value)
	}
	return request
}
