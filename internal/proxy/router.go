package proxy

import (
	"net/http"
)

// Handlers are the endpoint handlers a Router dispatches to once the
// method and query-string rules in this file have passed. Their bodies
// belong to other beads; this file owns only the routing envelope every
// request must clear first.
type Handlers struct {
	Models          http.HandlerFunc
	ChatCompletions http.HandlerFunc
}

// NewRouter returns the fixed, closed route table: exactly POST
// /v1/chat/completions and GET /v1/models. Every other path and method
// combination and every non-empty query string answers locally from the
// error envelope and never reaches h.
func NewRouter(h Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", guarded(http.MethodPost, h.ChatCompletions))
	mux.HandleFunc("/v1/models", guarded(http.MethodGet, h.Models))
	mux.HandleFunc("/", unknownPath)
	return mux
}

// guarded enforces method and query-string rules before delegating to next.
// It never registers HEAD or OPTIONS as accepted: this route table has
// exactly one valid method per resource, stated by method, not inferred
// from it. Rejections here answer anonymously: they were never requests
// addressed to this proxy.
func guarded(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			WriteError(w, "", ErrMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" {
			WriteError(w, "", ErrQueryNotSupported)
			return
		}
		next(w, r)
	}
}

// unknownPath answers every path outside the fixed route table, including
// a trailing-slash form of a canonical path, which net/http.ServeMux does
// not treat as an alias for the exact-match pattern registered above.
func unknownPath(w http.ResponseWriter, r *http.Request) {
	WriteError(w, "", ErrNotFound)
}
