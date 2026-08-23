package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// UpstreamBaseURL is the fixed upstream base URL. The scheme, host and path
// are source constants: no request-time input, environment variable or
// proxy can change what host a dispatch connects to.
const UpstreamBaseURL = "https://ollama.com/v1"

// upstreamAccountCount is the number of upstream accounts in the fixed
// catalog. The connection pool is sized for the worst case where every
// account is at its in-flight ceiling at once.
const upstreamAccountCount = 3

// NewUpstreamTransport returns the single shared *http.Transport used for
// every dispatch to every upstream account. Every property is set
// explicitly from internal/policy constants rather than left at whatever a
// given Go release defaults to, because a default that changes between
// releases is a security or latency regression nobody chose.
//
// Credentials are per-request headers, not per-connection state, so one
// pooled transport safely serves all three accounts.
func NewUpstreamTransport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(false)

	// The pool is sized for every account at its in-flight ceiling at
	// once. Go's default of two idle connections per host would make most
	// dispatches pay a fresh TLS handshake.
	maxConns := policy.InFlightAttemptsPerAccount * upstreamAccountCount

	return &http.Transport{
		// Proxy is nil so HTTPS_PROXY and ALL_PROXY in the process
		// environment have no effect: the destination is fixed in source
		// and must not be rerouted through whatever host an environment
		// variable names.
		Proxy: nil,

		// DialContext sets the dial timeout explicitly. KeepAlive matches
		// the standard transport's dialer default so the only change from
		// that dialer is the timeout.
		DialContext: (&net.Dialer{
			Timeout:   policy.UpstreamDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		// DisableCompression keeps whatever encoding upstream chose
		// reaching the client exactly as it arrived.
		DisableCompression: true,

		MaxIdleConns:           maxConns,
		MaxIdleConnsPerHost:    maxConns,
		MaxConnsPerHost:        maxConns,
		IdleConnTimeout:        policy.UpstreamIdleConnectionTimeout,
		TLSHandshakeTimeout:    policy.UpstreamTLSHandshakeTimeout,
		MaxResponseHeaderBytes: policy.MaxUpstreamResponseHeaderBytes,

		// ResponseHeaderTimeout is deliberately zero: a queued or
		// slow-starting generation is not a failure, and the logical
		// deadline is the only bound on the wait for headers.
		ResponseHeaderTimeout: 0,

		// Protocols enables HTTP/1.1 and HTTP/2 over TLS explicitly
		// rather than leaving them to defaults. Unencrypted HTTP/2 is
		// disabled because the upstream is HTTPS only.
		Protocols: protocols,

		// TLSClientConfig keeps certificate verification enabled (the
		// default) and pins the minimum TLS version.
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// NewUpstreamClient returns the single shared *http.Client built on the
// shared transport. Redirect following is disabled so an upstream 3xx or
// 101 is returned to the caller rather than followed; the caller converts
// it into a local 502 before commitment.
//
// The client carries no Timeout: the overall request lifetime is controlled
// by the request context, not a client-level field.
func NewUpstreamClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Transport:     transport,
		CheckRedirect: noRedirect,
	}
}

// noRedirect is the client redirect policy: it returns the response as-is
// instead of following it.
func noRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}
