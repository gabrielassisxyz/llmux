package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

// ModelObject is one entry in the /v1/models response.
type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the OpenAI-shaped /v1/models response envelope. The
// envelope is explicit rather than a bare array, because a client picker
// that reads data finds nothing if the array is returned bare, and that is
// a failure with no error attached to it.
type ModelList struct {
	Object string        `json:"object"`
	Data   []ModelObject `json:"data"`
}

// pinnedSuffixes is the fixed, ordered set of account-pinned suffixes
// appended after each base alias.
var pinnedSuffixes = [...]catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3}

// ServeModels handles GET /v1/models. It performs no upstream request, no
// database query and no account-health evaluation: its only collaborator
// is catalog.BaseRoutes, an in-memory, fixed source. Output is therefore
// stable regardless of account saturation, cooldown or disablement, since
// nothing about account state is ever read.
//
// Ordering is deterministic: for each base alias, in catalog order, the
// base alias is followed by its -k1, -k2 and -k3 variants. This is not the
// order catalog.Routes returns (all base aliases, then all variants); the
// grouped order is this endpoint's own contract.
func ServeModels(w http.ResponseWriter, r *http.Request) {
	base := catalog.BaseRoutes()
	data := make([]ModelObject, 0, len(base)*(1+len(pinnedSuffixes)))
	for _, route := range base {
		data = append(data, modelObject(route.Alias))
		for _, account := range pinnedSuffixes {
			data = append(data, modelObject(route.Alias+"-"+string(account)))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ModelList{Object: "list", Data: data})
}

// modelObject builds one response entry. created is always zero because
// the proxy has no meaningful creation timestamp and must not fabricate
// one.
func modelObject(alias string) ModelObject {
	return ModelObject{ID: alias, Object: "model", Created: 0, OwnedBy: "llmux"}
}
