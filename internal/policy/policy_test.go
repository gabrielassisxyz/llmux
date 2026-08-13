package policy

import (
	"testing"
	"time"
)

func TestRequestLifecycleValues(t *testing.T) {
	if LogicalRequestDeadline != 10*time.Minute {
		t.Errorf("LogicalRequestDeadline = %v, want 10m", LogicalRequestDeadline)
	}
	if ShutdownGrace != 10*time.Minute+10*time.Second {
		t.Errorf("ShutdownGrace = %v, want 10m10s", ShutdownGrace)
	}
	if MaxAccountAcquisitionTime != 60*time.Second {
		t.Errorf("MaxAccountAcquisitionTime = %v, want 60s", MaxAccountAcquisitionTime)
	}
}

func TestBodyAndMemoryBoundValues(t *testing.T) {
	if MaxRequestBodyBytes != 64*1024*1024 {
		t.Errorf("MaxRequestBodyBytes = %d, want 64 MiB", MaxRequestBodyBytes)
	}
	if PrecommitResponseBufferBytes != 8*1024*1024 {
		t.Errorf("PrecommitResponseBufferBytes = %d, want 8 MiB", PrecommitResponseBufferBytes)
	}
	if MaxJSONNestingDepth != 256 {
		t.Errorf("MaxJSONNestingDepth = %d, want 256", MaxJSONNestingDepth)
	}
	if AggregateMemoryBudgetBytes != 512*1024*1024 {
		t.Errorf("AggregateMemoryBudgetBytes = %d, want 512 MiB", AggregateMemoryBudgetBytes)
	}
	if UnknownLengthBodyChargeStepBytes != 1024*1024 {
		t.Errorf("UnknownLengthBodyChargeStepBytes = %d, want 1 MiB", UnknownLengthBodyChargeStepBytes)
	}
}

func TestAdmissionAndConnectionCeilingValues(t *testing.T) {
	if ConcurrentAdmittedChatRequests != 128 {
		t.Errorf("ConcurrentAdmittedChatRequests = %d, want 128", ConcurrentAdmittedChatRequests)
	}
	if LiveAcceptedClientConnections != 256 {
		t.Errorf("LiveAcceptedClientConnections = %d, want 256", LiveAcceptedClientConnections)
	}
	if GlobalRequestAdmissionWait != time.Second {
		t.Errorf("GlobalRequestAdmissionWait = %v, want 1s", GlobalRequestAdmissionWait)
	}
}

func TestSessionAffinityValues(t *testing.T) {
	if SessionAffinityTTL != time.Hour {
		t.Errorf("SessionAffinityTTL = %v, want 1h", SessionAffinityTTL)
	}
	if LiveSessionPins != 4096 {
		t.Errorf("LiveSessionPins = %d, want 4096", LiveSessionPins)
	}
	if SaturatedPinGrace != 5*time.Second {
		t.Errorf("SaturatedPinGrace = %v, want 5s", SaturatedPinGrace)
	}
}

// TestProvisionalPinLifetimeTracksRequestDeadline proves the two constants
// stay tied by reference: this fails if a future edit gives
// ProvisionalPinMaxLifetime its own literal instead of deriving it from
// LogicalRequestDeadline.
func TestProvisionalPinLifetimeTracksRequestDeadline(t *testing.T) {
	if ProvisionalPinMaxLifetime != LogicalRequestDeadline {
		t.Errorf("ProvisionalPinMaxLifetime = %v, want it to equal LogicalRequestDeadline (%v)",
			ProvisionalPinMaxLifetime, LogicalRequestDeadline)
	}
}

// TestFiveSecondConstantsAreIndependent documents that SaturatedPinGrace and
// MinDeadlineRunwayBeforeRetryDispatch are deliberately separate symbols
// despite sharing a value: this fails to compile, not fails at runtime, if
// either is ever collapsed into the other.
func TestFiveSecondConstantsAreIndependent(t *testing.T) {
	var _, _ = SaturatedPinGrace, MinDeadlineRunwayBeforeRetryDispatch
	if SaturatedPinGrace != MinDeadlineRunwayBeforeRetryDispatch {
		t.Fatalf("expected both five-second policies to currently share a value: got %v and %v",
			SaturatedPinGrace, MinDeadlineRunwayBeforeRetryDispatch)
	}
}

func TestPerAccountRateAndDispatchValues(t *testing.T) {
	if RollingRateWindow != 60*time.Second {
		t.Errorf("RollingRateWindow = %v, want 60s", RollingRateWindow)
	}
	if PostStartDispatchBlackout != 60*time.Second {
		t.Errorf("PostStartDispatchBlackout = %v, want 60s", PostStartDispatchBlackout)
	}
	if DispatchesPerWindowPerAccount != 60 {
		t.Errorf("DispatchesPerWindowPerAccount = %d, want 60", DispatchesPerWindowPerAccount)
	}
	if InFlightAttemptsPerAccount != 12 {
		t.Errorf("InFlightAttemptsPerAccount = %d, want 12", InFlightAttemptsPerAccount)
	}
	if MaxDispatchesPerLogicalRequest != 4 {
		t.Errorf("MaxDispatchesPerLogicalRequest = %d, want 4", MaxDispatchesPerLogicalRequest)
	}
	if MinDeadlineRunwayBeforeRetryDispatch != 5*time.Second {
		t.Errorf("MinDeadlineRunwayBeforeRetryDispatch = %v, want 5s", MinDeadlineRunwayBeforeRetryDispatch)
	}
}

func TestRelayAndObservationValues(t *testing.T) {
	if IntermediateResponseDrainCapBytes != 64*1024 {
		t.Errorf("IntermediateResponseDrainCapBytes = %d, want 64 KiB", IntermediateResponseDrainCapBytes)
	}
	if SSEObserverLineCapBytes != 1024*1024 {
		t.Errorf("SSEObserverLineCapBytes = %d, want 1 MiB", SSEObserverLineCapBytes)
	}
	if ObserverCumulativeDecodedOutputCapBytes != 64*1024*1024 {
		t.Errorf("ObserverCumulativeDecodedOutputCapBytes = %d, want 64 MiB", ObserverCumulativeDecodedOutputCapBytes)
	}
}

func TestDurableStoreValues(t *testing.T) {
	if SQLiteBusyTimeout != 5*time.Second {
		t.Errorf("SQLiteBusyTimeout = %v, want 5s", SQLiteBusyTimeout)
	}
	if StoreOperationCeiling != 6*time.Second {
		t.Errorf("StoreOperationCeiling = %v, want 6s", StoreOperationCeiling)
	}
	if PassiveCheckpointIntervalCommits != 256 {
		t.Errorf("PassiveCheckpointIntervalCommits = %d, want 256", PassiveCheckpointIntervalCommits)
	}
	if WALSizeWarningThresholdBytes != 64*1024*1024 {
		t.Errorf("WALSizeWarningThresholdBytes = %d, want 64 MiB", WALSizeWarningThresholdBytes)
	}
}

func TestHTTPServerValues(t *testing.T) {
	if ServerHeaderReadTimeout != 5*time.Second {
		t.Errorf("ServerHeaderReadTimeout = %v, want 5s", ServerHeaderReadTimeout)
	}
	if ServerRequestReadTimeout != 2*time.Minute {
		t.Errorf("ServerRequestReadTimeout = %v, want 2m", ServerRequestReadTimeout)
	}
	if ServerIdleTimeout != 2*time.Minute {
		t.Errorf("ServerIdleTimeout = %v, want 2m", ServerIdleTimeout)
	}
	if DownstreamWriteDeadline != 30*time.Second {
		t.Errorf("DownstreamWriteDeadline = %v, want 30s", DownstreamWriteDeadline)
	}
	if MaxRequestHeaderBytes != 64*1024 {
		t.Errorf("MaxRequestHeaderBytes = %d, want 64 KiB", MaxRequestHeaderBytes)
	}
}

func TestUpstreamTransportValues(t *testing.T) {
	if MaxUpstreamResponseHeaderBytes != 128*1024 {
		t.Errorf("MaxUpstreamResponseHeaderBytes = %d, want 128 KiB", MaxUpstreamResponseHeaderBytes)
	}
	if UpstreamDialTimeout != 10*time.Second {
		t.Errorf("UpstreamDialTimeout = %v, want 10s", UpstreamDialTimeout)
	}
	if UpstreamTLSHandshakeTimeout != 10*time.Second {
		t.Errorf("UpstreamTLSHandshakeTimeout = %v, want 10s", UpstreamTLSHandshakeTimeout)
	}
	if UpstreamIdleConnectionTimeout != 90*time.Second {
		t.Errorf("UpstreamIdleConnectionTimeout = %v, want 90s", UpstreamIdleConnectionTimeout)
	}
}
