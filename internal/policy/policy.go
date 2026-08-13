// Package policy holds the fixed tuning constants this proxy runs on: request
// and shutdown deadlines, body and memory bounds, session and rate policy,
// store timeouts, and server and upstream transport timeouts.
//
// These are implementation constants, not a generic tuning surface. Each is
// defined exactly once here; every other place a value appears is expected to
// reference the constant, not restate the number.
//
// None of these is an environment variable. This process runs on one known
// machine, GOMEMLIMIT is already the host-level memory control that the Go
// runtime reads without help, and a second memory knob would only make it
// possible to configure the two into disagreement.
package policy

import "time"

// Request lifecycle.
const (
	// LogicalRequestDeadline bounds the wall-clock lifetime of one client
	// request, end to end, across every retry dispatch.
	LogicalRequestDeadline = 10 * time.Minute

	// ShutdownGrace is the wall-clock window after the first shutdown signal
	// in which in-flight requests may finish before the process exits. It
	// exceeds LogicalRequestDeadline by ten seconds so a request admitted a
	// moment before the signal still has room to reach its own deadline.
	ShutdownGrace = 10*time.Minute + 10*time.Second

	// MaxAccountAcquisitionTime bounds how long one dispatch attempt may wait
	// to acquire an account lease before giving up.
	MaxAccountAcquisitionTime = 60 * time.Second
)

// Body and memory bounds.
const (
	// MaxRequestBodyBytes is the largest request body the proxy will read.
	MaxRequestBodyBytes = 64 * 1024 * 1024 // 64 MiB

	// PrecommitResponseBufferBytes bounds the buffer held for a
	// non-streaming response before it is committed downstream.
	PrecommitResponseBufferBytes = 8 * 1024 * 1024 // 8 MiB

	// MaxJSONNestingDepth bounds the nesting depth the top-level scanner
	// accepts.
	MaxJSONNestingDepth = 256

	// AggregateMemoryBudgetBytes is the process-wide ceiling on request,
	// replay and precommit buffers combined. The runbook recommends a budget
	// no larger than 60% of GOMEMLIMIT, leaving headroom for the runtime,
	// SQLite, goroutine stacks and transport buffers.
	AggregateMemoryBudgetBytes = 512 * 1024 * 1024 // 512 MiB

	// UnknownLengthBodyChargeStepBytes is the increment by which a body of
	// unknown length is charged against the aggregate memory budget as it is
	// read.
	UnknownLengthBodyChargeStepBytes = 1024 * 1024 // 1 MiB
)

// Admission and connection ceilings.
const (
	// ConcurrentAdmittedChatRequests bounds how many chat requests may be
	// admitted for processing at once.
	ConcurrentAdmittedChatRequests = 128

	// LiveAcceptedClientConnections bounds how many accepted client
	// connections may be live at once.
	LiveAcceptedClientConnections = 256

	// GlobalRequestAdmissionWait bounds how long a request may wait for an
	// admission slot before being rejected.
	GlobalRequestAdmissionWait = 1 * time.Second
)

// Session affinity.
const (
	// SessionAffinityTTL is how long a session pin remains valid after its
	// last use.
	SessionAffinityTTL = 1 * time.Hour

	// LiveSessionPins bounds how many session pins may be held at once.
	LiveSessionPins = 4096

	// ProvisionalPinMaxLifetime bounds how long a provisional pin may live
	// before it is confirmed or discarded. It equals LogicalRequestDeadline
	// rather than restating it, because a provisional pin cannot outlive the
	// request that created it.
	ProvisionalPinMaxLifetime = LogicalRequestDeadline

	// SaturatedPinGrace is the extra time a request may wait for its pinned
	// account when that account is saturated, before falling back to
	// selection. This is unrelated to
	// MinDeadlineRunwayBeforeRetryDispatch even though both are five
	// seconds: collapsing them into one constant would couple two
	// independent policies that happen to share a value today.
	SaturatedPinGrace = 5 * time.Second
)

// Per-account rate and dispatch policy.
const (
	// RollingRateWindow is the width of the rolling window over which
	// dispatches per account are counted.
	RollingRateWindow = 60 * time.Second

	// PostStartDispatchBlackout is the window after process start during
	// which no dispatch is sent to any account.
	PostStartDispatchBlackout = 60 * time.Second

	// DispatchesPerWindowPerAccount is a self-imposed guard, not a published
	// Ollama Cloud limit; no such limit is documented. Upstream 429 is the
	// only authoritative statement of the real limit, and the design reacts
	// to it rather than trying to prevent it: sustained 429s under this
	// ceiling prove it is set too high, while silence proves nothing. This
	// value is expected to be re-derived from real traffic by the
	// post-cutover review, not tuned ahead of evidence.
	DispatchesPerWindowPerAccount = 60

	// InFlightAttemptsPerAccount is the companion self-imposed guard to
	// DispatchesPerWindowPerAccount, for the same reason and under the same
	// review. It also bounds concurrent upstream work per account, since
	// every live attempt holds its replay body and, for a non-streaming
	// response, up to a PrecommitResponseBufferBytes buffer.
	InFlightAttemptsPerAccount = 12

	// MaxDispatchesPerLogicalRequest bounds how many upstream dispatch
	// attempts one logical request may consume across all retries.
	MaxDispatchesPerLogicalRequest = 4

	// MinDeadlineRunwayBeforeRetryDispatch is the minimum time remaining
	// before LogicalRequestDeadline for a retry dispatch to be attempted at
	// all.
	MinDeadlineRunwayBeforeRetryDispatch = 5 * time.Second
)

// Relay and usage observation.
const (
	// IntermediateResponseDrainCapBytes bounds how much of a superseded
	// response body is drained before it is discarded.
	IntermediateResponseDrainCapBytes = 64 * 1024 // 64 KiB

	// SSEObserverLineCapBytes bounds the length of a single observed
	// server-sent-event line.
	SSEObserverLineCapBytes = 1024 * 1024 // 1 MiB

	// ObserverCumulativeDecodedOutputCapBytes bounds the total decoded
	// output the usage observer will read for one response.
	ObserverCumulativeDecodedOutputCapBytes = 64 * 1024 * 1024 // 64 MiB per response
)

// Durable store.
const (
	// SQLiteBusyTimeout bounds how long a store operation waits on a locked
	// database before failing.
	SQLiteBusyTimeout = 5 * time.Second

	// StoreOperationCeiling bounds the total time one store operation may
	// take, independent of the busy timeout.
	StoreOperationCeiling = 6 * time.Second

	// PassiveCheckpointIntervalCommits is how many terminal commits pass
	// between passive WAL checkpoints.
	PassiveCheckpointIntervalCommits = 256

	// WALSizeWarningThresholdBytes is the WAL file size above which the
	// store logs a warning.
	WALSizeWarningThresholdBytes = 64 * 1024 * 1024 // 64 MiB
)

// HTTP server.
const (
	// ServerHeaderReadTimeout bounds how long the server waits to read
	// request headers.
	ServerHeaderReadTimeout = 5 * time.Second

	// ServerRequestReadTimeout bounds how long the server waits to read a
	// full request.
	ServerRequestReadTimeout = 2 * time.Minute

	// ServerIdleTimeout bounds how long the server keeps an idle
	// keep-alive connection open.
	ServerIdleTimeout = 2 * time.Minute

	// DownstreamWriteDeadline is armed fresh before each write to the
	// client, rather than once for the whole response.
	DownstreamWriteDeadline = 30 * time.Second

	// MaxRequestHeaderBytes bounds the total size of request headers the
	// server accepts.
	MaxRequestHeaderBytes = 64 * 1024 // 64 KiB
)

// Upstream transport.
const (
	// MaxUpstreamResponseHeaderBytes bounds the total size of response
	// headers accepted from an upstream account.
	MaxUpstreamResponseHeaderBytes = 128 * 1024 // 128 KiB

	// UpstreamDialTimeout bounds how long a TCP dial to an upstream account
	// may take.
	UpstreamDialTimeout = 10 * time.Second

	// UpstreamTLSHandshakeTimeout bounds how long a TLS handshake with an
	// upstream account may take.
	UpstreamTLSHandshakeTimeout = 10 * time.Second

	// UpstreamIdleConnectionTimeout bounds how long an idle pooled
	// connection to an upstream account is kept open.
	UpstreamIdleConnectionTimeout = 90 * time.Second
)
