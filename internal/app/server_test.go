package app

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestNewServerFieldsMatchPolicy(t *testing.T) {
	handler := http.NewServeMux()
	srv := NewServer("127.0.0.1:0", handler)

	if srv.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q, want %q", srv.Addr, "127.0.0.1:0")
	}
	if h, _ := srv.Handler.(*http.ServeMux); h != handler {
		t.Error("Handler was not wired to the provided handler")
	}
	if srv.ReadHeaderTimeout != policy.ServerHeaderReadTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, policy.ServerHeaderReadTimeout)
	}
	if srv.ReadTimeout != policy.ServerRequestReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, policy.ServerRequestReadTimeout)
	}
	if srv.IdleTimeout != policy.ServerIdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, policy.ServerIdleTimeout)
	}
	if srv.MaxHeaderBytes != policy.MaxRequestHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, policy.MaxRequestHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (disabled)", srv.WriteTimeout)
	}
	if srv.Protocols == nil {
		t.Fatal("Protocols must be set explicitly, not left nil")
	}
	if !srv.Protocols.HTTP1() {
		t.Error("Protocols.HTTP1() = false, want true")
	}
	if srv.Protocols.HTTP2() {
		t.Error("Protocols.HTTP2() = true, want false")
	}
	if srv.Protocols.UnencryptedHTTP2() {
		t.Error("Protocols.UnencryptedHTTP2() = true, want false")
	}
}

// startTestServer starts srv on a loopback listener chosen by the OS and
// returns its address and a cleanup func. The listener is created directly,
// rather than through srv.ListenAndServe, so the test can learn the
// ephemeral port before issuing requests.
func startTestServer(t *testing.T, handler http.Handler) (addr string, cleanup func()) {
	t.Helper()
	srv := NewServer("127.0.0.1:0", handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		_ = srv.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		_ = srv.Close()
	}
}

func TestNewServerAcceptsHTTP1Requests(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("request ProtoMajor = %d, want 1", r.ProtoMajor)
		}
		w.WriteHeader(http.StatusOK)
	})
	addr, cleanup := startTestServer(t, handler)
	defer cleanup()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if resp.ProtoMajor != 1 {
		t.Errorf("response ProtoMajor = %d, want 1", resp.ProtoMajor)
	}
}

// TestNewServerDoesNotSpeakUnencryptedHTTP2 sends the literal HTTP/2
// connection preface (RFC 9113 section 3.4) over a plaintext connection and
// proves the server never replies with real HTTP/2 framing. A genuine h2
// connection always opens with a 9-byte frame header whose fourth byte (the
// frame type) is 0x04 for SETTINGS; UnencryptedHTTP2 being disabled in
// NewServer means that branch of the server is never reached at all,
// regardless of how the HTTP/1.x parser downstream happens to interpret
// those bytes.
func TestNewServerDoesNotSpeakUnencryptedHTTP2(t *testing.T) {
	addr, cleanup := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer cleanup()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	const http2ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	if _, err := conn.Write([]byte(http2ClientPreface)); err != nil {
		t.Fatalf("write preface: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	reply := make([]byte, 9)
	n, _ := io.ReadFull(conn, reply)
	if n >= 4 && reply[3] == 0x04 {
		t.Fatalf("received what looks like an HTTP/2 SETTINGS frame: % x", reply[:n])
	}
}

// TestServerReadHeaderTimeoutBoundsASlowClient proves the mechanism this
// bead relies on for "a slow client does not hold a handler indefinitely":
// http.Server.ReadHeaderTimeout. It deliberately does not go through
// NewServer, whose ReadHeaderTimeout is the real five-second policy
// constant; waiting five real seconds in a unit test for a value NewServer
// already asserts by direct field comparison would only slow the suite
// without proving anything more.
func TestServerReadHeaderTimeoutBoundsASlowClient(t *testing.T) {
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 100 * time.Millisecond,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(listener) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a request line and nothing else: headers never complete.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write partial request: %v", err)
	}

	// The outer read deadline is deliberately much larger than
	// ReadHeaderTimeout: it exists only so a broken mechanism fails this
	// test instead of hanging it. What proves the mechanism works is the
	// elapsed-time assertion below, not merely that the read eventually
	// returns.
	const outerDeadline = 2 * time.Second
	if err := conn.SetReadDeadline(time.Now().Add(outerDeadline)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	start := time.Now()
	buf := make([]byte, 512)
	n, readErr := conn.Read(buf)
	elapsed := time.Since(start)

	const boundedFailureCeiling = 1 * time.Second
	if elapsed >= boundedFailureCeiling {
		t.Fatalf("connection was still open after %v; ReadHeaderTimeout (100ms) did not bound the slow client", elapsed)
	}
	if readErr != nil && n == 0 {
		// The connection was closed with no response, which is a bounded
		// failure: the handler was never reached and the client did not
		// hang.
		return
	}
	if !bytes.Contains(buf[:n], []byte("408")) && !bytes.Contains(buf[:n], []byte("HTTP/1.1")) {
		t.Fatalf("unexpected response to a client that never finished its headers: %q", buf[:n])
	}
}

// TestServerReadTimeoutBoundsASlowBody proves the mechanism this bead
// relies on for the acceptance criterion distinct from the header case
// above: http.Server.ReadTimeout, which "bounds receipt of the complete
// request body. Without it a client that trickles a body holds a handler
// and its buffered body for as long as it likes: neither the logical
// context nor client cancellation interrupts a body read, because a slow
// client has not disconnected." This test sends complete headers (so
// ReadHeaderTimeout is satisfied) declaring a body the client then never
// finishes sending, and asserts the handler's own body read is the thing
// that unblocks, not merely that the connection eventually closes.
func TestServerReadTimeoutBoundsASlowBody(t *testing.T) {
	bodyReadErr := make(chan error, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			bodyReadErr <- err
		}),
		ReadTimeout: 100 * time.Millisecond,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(listener) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Declare a 1000-byte body, then never send it: the handler's Body
	// read has something to legitimately wait on, distinguishing this from
	// a client that simply never speaks at all.
	request := "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 1000\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write request with undelivered body: %v", err)
	}

	select {
	case err := <-bodyReadErr:
		if err == nil {
			t.Fatal("expected the handler's body read to fail once ReadTimeout elapsed, got nil error")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("handler's body read did not return within 1s of a 100ms ReadTimeout; the timeout did not bound the slow body")
	}
}

// TestServerMaxHeaderBytesRejectsOversizeHeaders proves the mechanism this
// bead relies on for "oversize headers produce the standard bounded server
// failure". It runs against the real NewServer with the real 64 KiB policy
// constant: exceeding a byte ceiling needs no wall-clock wait, so there is
// no reason to shrink it for the test the way the read-timeout test above
// must.
func TestServerMaxHeaderBytesRejectsOversizeHeaders(t *testing.T) {
	addr, cleanup := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer cleanup()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// net/http reads MaxHeaderBytes plus a fixed 4096-byte bufio slop
	// (see initialReadLimitSize in the standard library) before it gives
	// up, so the oversize margin here must clear that slop to reliably
	// trip the limit.
	oversizeValue := strings.Repeat("a", policy.MaxRequestHeaderBytes+8192)
	request := "GET / HTTP/1.1\r\nHost: test\r\nX-Oversize: " + oversizeValue + "\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write oversize request: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil && len(reply) == 0 {
		t.Fatalf("reading response: %v", err)
	}
	if !bytes.Contains(reply, []byte("431")) {
		t.Fatalf("response to an oversize header did not carry status 431: %q", reply)
	}
}
