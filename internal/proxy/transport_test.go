package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestNewUpstreamTransportFieldsMatchPolicy(t *testing.T) {
	transport := NewUpstreamTransport()

	if transport.Proxy != nil {
		t.Error("Proxy must be nil so environment proxy variables are ignored")
	}
	if transport.DialContext == nil {
		t.Error("DialContext must be set so the dial timeout is explicit")
	}
	if !transport.DisableCompression {
		t.Error("DisableCompression must be true so responses are not transparently decompressed")
	}

	wantConns := policy.InFlightAttemptsPerAccount * upstreamAccountCount
	if transport.MaxIdleConns != wantConns {
		t.Errorf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, wantConns)
	}
	if transport.MaxIdleConnsPerHost != wantConns {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, wantConns)
	}
	if transport.MaxConnsPerHost != wantConns {
		t.Errorf("MaxConnsPerHost = %d, want %d", transport.MaxConnsPerHost, wantConns)
	}
	if transport.IdleConnTimeout != policy.UpstreamIdleConnectionTimeout {
		t.Errorf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, policy.UpstreamIdleConnectionTimeout)
	}
	if transport.TLSHandshakeTimeout != policy.UpstreamTLSHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", transport.TLSHandshakeTimeout, policy.UpstreamTLSHandshakeTimeout)
	}
	if transport.MaxResponseHeaderBytes != policy.MaxUpstreamResponseHeaderBytes {
		t.Errorf("MaxResponseHeaderBytes = %d, want %d", transport.MaxResponseHeaderBytes, policy.MaxUpstreamResponseHeaderBytes)
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want 0 (deliberately unbounded)", transport.ResponseHeaderTimeout)
	}

	if transport.Protocols == nil {
		t.Fatal("Protocols must be set explicitly, not left nil")
	}
	if !transport.Protocols.HTTP1() {
		t.Error("Protocols.HTTP1() = false, want true")
	}
	if !transport.Protocols.HTTP2() {
		t.Error("Protocols.HTTP2() = false, want true")
	}
	if transport.Protocols.UnencryptedHTTP2() {
		t.Error("Protocols.UnencryptedHTTP2() = true, want false")
	}

	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must be set explicitly")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("TLSClientConfig.MinVersion = %#x, want %#x", transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("TLSClientConfig.InsecureSkipVerify = true, want false (certificate verification enabled)")
	}
}

func TestNewUpstreamClientFields(t *testing.T) {
	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	if client.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 (request context controls lifetime)", client.Timeout)
	}
	if client.Transport != transport {
		t.Error("Transport was not wired to the provided transport")
	}
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set to disable redirect following")
	}
	if err := client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect returned %v, want http.ErrUseLastResponse", err)
	}
}

// trustServerCert adds the test server's self-signed certificate to the
// transport's trust pool, preserving the transport's own TLS settings
// (minimum version, verification enabled). It exists only so a hermetic
// HTTPS test can reach a local server without weakening the transport.
func trustServerCert(t *testing.T, transport *http.Transport, server *httptest.Server) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = pool
}

func TestUpstreamTransportIgnoresProxyEnvironment(t *testing.T) {
	var sinkHits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkHits.Add(1)
	}))
	defer sink.Close()

	var upstreamHits atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Setenv("HTTPS_PROXY", sink.URL)
	t.Setenv("ALL_PROXY", sink.URL)

	transport := NewUpstreamTransport()
	trustServerCert(t, transport, upstream)
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if got := upstreamHits.Load(); got != 1 {
		t.Errorf("upstream received %d request(s), want 1", got)
	}
	if got := sinkHits.Load(); got != 0 {
		t.Errorf("proxy sink received %d connection(s); the transport honored HTTPS_PROXY/ALL_PROXY", got)
	}
}

func TestUpstreamTransportRejectsOversizedResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Oversize", strings.Repeat("a", policy.MaxUpstreamResponseHeaderBytes+1))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected an error for oversized response headers, got nil")
	}
}

func TestUpstreamTransportAcceptsHeadersWithinBound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Large", strings.Repeat("a", policy.MaxUpstreamResponseHeaderBytes/2))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
}

func TestUpstreamClientDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer upstream.Close()

	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d (redirect not followed)", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != target.URL {
		t.Errorf("Location = %q, want %q", loc, target.URL)
	}
	if got := targetHits.Load(); got != 0 {
		t.Errorf("redirect target received %d request(s); the client followed the redirect", got)
	}
}

func TestUpstreamTransportDoesNotDecompressResponses(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte("hello compressed")); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer upstream.Close()

	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Uncompressed {
		t.Error("response was transparently decompressed; DisableCompression was not honored")
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want %q", ce, "gzip")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if !bytes.Equal(body, compressed.Bytes()) {
		t.Error("response body was altered; want the exact compressed bytes")
	}
}

func TestUpstreamTransportCloseIdleConnections(t *testing.T) {
	closed := make(chan struct{}, 1)

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	upstream.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed || state == http.StateHijacked {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	upstream.Start()
	defer upstream.Close()

	transport := NewUpstreamTransport()
	client := NewUpstreamClient(transport)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// The connection becomes idle in the pool asynchronously after the
	// body is closed, so retry CloseIdleConnections briefly until the
	// server observes the close.
	deadline := time.Now().Add(2 * time.Second)
	for {
		transport.CloseIdleConnections()
		select {
		case <-closed:
			return
		case <-time.After(20 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("idle upstream connection was not closed by CloseIdleConnections")
			}
		}
	}
}
