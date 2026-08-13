package app

import (
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// NewServer returns a fully configured *http.Server for addr, serving
// handler. It speaks HTTP/1.x only, configured explicitly through
// http.Server.Protocols rather than left to whatever a given Go release
// defaults to: cleartext HTTP/2 requires prior knowledge that none of this
// proxy's consumers use, and restricting it keeps the committed-response
// abort semantics on the relay path to one case instead of two, the second
// of which could never be exercised here.
//
// WriteTimeout is left disabled (its zero value) because an absolute write
// deadline is incompatible with valid long-lived streams; per-write
// deadlines are armed through http.ResponseController in the relay path
// instead.
func NewServer(addr string, handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(false)
	protocols.SetUnencryptedHTTP2(false)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: policy.ServerHeaderReadTimeout,
		ReadTimeout:       policy.ServerRequestReadTimeout,
		IdleTimeout:       policy.ServerIdleTimeout,
		MaxHeaderBytes:    policy.MaxRequestHeaderBytes,
	}
}
