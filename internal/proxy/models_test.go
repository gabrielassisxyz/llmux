package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

func decodeModelList(t *testing.T, body []byte) ModelList {
	t.Helper()
	var list ModelList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decoding model list: %v, body: %s", err, body)
	}
	return list
}

func serveModels(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ServeModels(rec, req)
	return rec
}

// expectedAliasesInGroupedOrder recomputes the expected alias order from
// catalog.BaseRoutes directly, rather than reusing ServeModels's own
// grouping loop, so this test does not become tautological.
func expectedAliasesInGroupedOrder(t *testing.T) []string {
	t.Helper()
	var want []string
	for _, route := range catalog.BaseRoutes() {
		want = append(want, route.Alias)
		want = append(want, route.Alias+"-k1", route.Alias+"-k2", route.Alias+"-k3")
	}
	return want
}

func TestServeModelsReturnsAllAliasesInGroupedOrder(t *testing.T) {
	rec := serveModels(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	list := decodeModelList(t, rec.Body.Bytes())

	want := expectedAliasesInGroupedOrder(t)
	if len(list.Data) != len(want) {
		t.Fatalf("got %d models, want %d", len(list.Data), len(want))
	}
	if len(want) != 28 {
		t.Fatalf("test setup produced %d expected aliases, want 28", len(want))
	}
	for i, model := range list.Data {
		if model.ID != want[i] {
			t.Errorf("position %d: id = %q, want %q", i, model.ID, want[i])
		}
	}
}

func TestServeModelsFieldValues(t *testing.T) {
	rec := serveModels(t)
	list := decodeModelList(t, rec.Body.Bytes())

	if len(list.Data) == 0 {
		t.Fatal("no models returned")
	}
	for _, model := range list.Data {
		if model.Object != "model" {
			t.Errorf("%s: object = %q, want %q", model.ID, model.Object, "model")
		}
		if model.Created != 0 {
			t.Errorf("%s: created = %d, want 0", model.ID, model.Created)
		}
		if model.OwnedBy != "llmux" {
			t.Errorf("%s: owned_by = %q, want %q", model.ID, model.OwnedBy, "llmux")
		}
	}
}

func TestServeModelsCreatedFieldIsPresentAsZeroNotOmitted(t *testing.T) {
	rec := serveModels(t)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"created":0`)) {
		t.Fatalf("response does not carry an explicit created:0 field: %s", rec.Body.String())
	}
}

func TestServeModelsEnvelopeIsObjectNotBareArray(t *testing.T) {
	rec := serveModels(t)
	body := bytes.TrimSpace(rec.Body.Bytes())
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("response is not a JSON object envelope: %s", body)
	}

	list := decodeModelList(t, rec.Body.Bytes())
	if list.Object != "list" {
		t.Errorf("object = %q, want %q", list.Object, "list")
	}
}

func TestServeModelsSetsJSONContentType(t *testing.T) {
	rec := serveModels(t)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestServeModelsIsDeterministicAcrossCalls proves the projection has no
// hidden mutable state: repeated calls in the same process produce
// byte-identical output. ServeModels's only collaborator is
// catalog.BaseRoutes, an in-memory fixed source with no account-health
// input, so this is the strongest determinism proof available until an
// account coordinator exists to actually drive saturated, cooling and
// disabled states.
func TestServeModelsIsDeterministicAcrossCalls(t *testing.T) {
	first := serveModels(t).Body.Bytes()
	for i := 0; i < 5; i++ {
		next := serveModels(t).Body.Bytes()
		if !bytes.Equal(first, next) {
			t.Fatalf("call %d produced different output:\nfirst: %s\nnext:  %s", i, first, next)
		}
	}
}
