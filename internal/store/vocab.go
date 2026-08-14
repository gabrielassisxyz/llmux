package store

// Outcome is the terminal result recorded on every attempt_log row.
type Outcome string

const (
	OutcomeSucceeded          Outcome = "succeeded"
	OutcomeUpstreamHTTPError  Outcome = "upstream_http_error"
	OutcomeTransportError     Outcome = "transport_error"
	OutcomeDeadlineExceeded   Outcome = "deadline_exceeded"
	OutcomeClientCanceled     Outcome = "client_canceled"
	OutcomeResponseReadError  Outcome = "response_read_error"
	OutcomeResponseWriteError Outcome = "response_write_error"
	OutcomeSelectionSkipped   Outcome = "selection_skipped"
	OutcomeCapacityTimeout    Outcome = "capacity_timeout"
	OutcomeNoAccountAvailable Outcome = "no_account_available"
	OutcomeInternalError      Outcome = "internal_error"
)

// Valid reports whether the value is one of the closed Outcome values.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeSucceeded, OutcomeUpstreamHTTPError, OutcomeTransportError,
		OutcomeDeadlineExceeded, OutcomeClientCanceled, OutcomeResponseReadError,
		OutcomeResponseWriteError, OutcomeSelectionSkipped, OutcomeCapacityTimeout,
		OutcomeNoAccountAvailable, OutcomeInternalError:
		return true
	}
	return false
}

// AllOutcomes returns the closed set of valid outcome values.
func AllOutcomes() []Outcome {
	return []Outcome{
		OutcomeSucceeded,
		OutcomeUpstreamHTTPError,
		OutcomeTransportError,
		OutcomeDeadlineExceeded,
		OutcomeClientCanceled,
		OutcomeResponseReadError,
		OutcomeResponseWriteError,
		OutcomeSelectionSkipped,
		OutcomeCapacityTimeout,
		OutcomeNoAccountAvailable,
		OutcomeInternalError,
	}
}

// ErrorClass classifies a failure on a dispatch attempt. It is nullable on the
// row; an empty string means no class is recorded.
type ErrorClass string

const (
	ErrorClassRateLimited             ErrorClass = "rate_limited"
	ErrorClassUpstreamAuthentication  ErrorClass = "upstream_authentication"
	ErrorClassUpstreamClientError     ErrorClass = "upstream_client_error"
	ErrorClassUpstreamServerError     ErrorClass = "upstream_server_error"
	ErrorClassInvalidUpstreamResponse ErrorClass = "invalid_upstream_response"
	ErrorClassTransportTimeout        ErrorClass = "transport_timeout"
	ErrorClassTransportTransient      ErrorClass = "transport_transient"
	ErrorClassTransportPermanent      ErrorClass = "transport_permanent"
	ErrorClassClientDisconnect        ErrorClass = "client_disconnect"
	ErrorClassResponseTruncated       ErrorClass = "response_truncated"
	ErrorClassLocalDeadline           ErrorClass = "local_deadline"
	ErrorClassAccountDisabled         ErrorClass = "account_disabled"
	ErrorClassAccountCooldown         ErrorClass = "account_cooldown"
	ErrorClassLocalCapacity           ErrorClass = "local_capacity"
)

// Valid reports whether the value is one of the closed ErrorClass values.
// The empty string is not valid; it represents a NULL column value.
func (e ErrorClass) Valid() bool {
	switch e {
	case ErrorClassRateLimited, ErrorClassUpstreamAuthentication, ErrorClassUpstreamClientError,
		ErrorClassUpstreamServerError, ErrorClassInvalidUpstreamResponse, ErrorClassTransportTimeout,
		ErrorClassTransportTransient, ErrorClassTransportPermanent, ErrorClassClientDisconnect,
		ErrorClassResponseTruncated, ErrorClassLocalDeadline, ErrorClassAccountDisabled,
		ErrorClassAccountCooldown, ErrorClassLocalCapacity:
		return true
	}
	return false
}

// AllErrorClasses returns the closed set of valid error-class values.
func AllErrorClasses() []ErrorClass {
	return []ErrorClass{
		ErrorClassRateLimited,
		ErrorClassUpstreamAuthentication,
		ErrorClassUpstreamClientError,
		ErrorClassUpstreamServerError,
		ErrorClassInvalidUpstreamResponse,
		ErrorClassTransportTimeout,
		ErrorClassTransportTransient,
		ErrorClassTransportPermanent,
		ErrorClassClientDisconnect,
		ErrorClassResponseTruncated,
		ErrorClassLocalDeadline,
		ErrorClassAccountDisabled,
		ErrorClassAccountCooldown,
		ErrorClassLocalCapacity,
	}
}

// SkipReason is recorded only on selection_skip rows.
type SkipReason string

const (
	SkipReasonRPMLimit      SkipReason = "rpm_limit"
	SkipReasonInFlightLimit SkipReason = "in_flight_limit"
	SkipReasonRateGated     SkipReason = "rate_gated"
	SkipReasonDisabled      SkipReason = "disabled"
	SkipReasonStartBlackout SkipReason = "start_blackout"
)

// Valid reports whether the value is one of the closed SkipReason values.
func (s SkipReason) Valid() bool {
	switch s {
	case SkipReasonRPMLimit, SkipReasonInFlightLimit, SkipReasonRateGated,
		SkipReasonDisabled, SkipReasonStartBlackout:
		return true
	}
	return false
}

// AllSkipReasons returns the closed set of valid skip-reason values.
func AllSkipReasons() []SkipReason {
	return []SkipReason{
		SkipReasonRPMLimit,
		SkipReasonInFlightLimit,
		SkipReasonRateGated,
		SkipReasonDisabled,
		SkipReasonStartBlackout,
	}
}

// RetryDisposition records what the dispatch logic decided about retrying.
type RetryDisposition string

const (
	RetryDispositionFinal                  RetryDisposition = "final"
	RetryDispositionRetrySameAccount       RetryDisposition = "retry_same_account"
	RetryDispositionRetryOtherAccount      RetryDisposition = "retry_other_account"
	RetryDispositionRetryNamedAccount      RetryDisposition = "retry_named_account"
	RetryDispositionSuppressedClassBudget  RetryDisposition = "suppressed_class_budget"
	RetryDispositionSuppressedGlobalBudget RetryDisposition = "suppressed_global_budget"
	RetryDispositionSuppressedDeadline     RetryDisposition = "suppressed_deadline"
	RetryDispositionNotApplicable          RetryDisposition = "not_applicable"
)

// Valid reports whether the value is one of the closed RetryDisposition values.
func (r RetryDisposition) Valid() bool {
	switch r {
	case RetryDispositionFinal, RetryDispositionRetrySameAccount,
		RetryDispositionRetryOtherAccount, RetryDispositionRetryNamedAccount,
		RetryDispositionSuppressedClassBudget, RetryDispositionSuppressedGlobalBudget,
		RetryDispositionSuppressedDeadline, RetryDispositionNotApplicable:
		return true
	}
	return false
}

// AllRetryDispositions returns the closed set of valid retry-disposition values.
func AllRetryDispositions() []RetryDisposition {
	return []RetryDisposition{
		RetryDispositionFinal,
		RetryDispositionRetrySameAccount,
		RetryDispositionRetryOtherAccount,
		RetryDispositionRetryNamedAccount,
		RetryDispositionSuppressedClassBudget,
		RetryDispositionSuppressedGlobalBudget,
		RetryDispositionSuppressedDeadline,
		RetryDispositionNotApplicable,
	}
}

// UsageObservation classifies what the usage observer was able to record on a
// dispatch attempt. It is nullable on the row, but non-NULL for every dispatch
// row; empty string means no value is recorded.
type UsageObservation string

const (
	UsageObservationNotApplicable       UsageObservation = "not_applicable"
	UsageObservationAbsent              UsageObservation = "absent"
	UsageObservationComplete            UsageObservation = "complete"
	UsageObservationMalformed           UsageObservation = "malformed"
	UsageObservationTruncated           UsageObservation = "truncated"
	UsageObservationUnsupportedEncoding UsageObservation = "unsupported_encoding"
	UsageObservationLimitExceeded       UsageObservation = "limit_exceeded"
)

// Valid reports whether the value is one of the closed UsageObservation values.
// The empty string is not valid; it represents a NULL column value.
func (u UsageObservation) Valid() bool {
	switch u {
	case UsageObservationNotApplicable, UsageObservationAbsent, UsageObservationComplete,
		UsageObservationMalformed, UsageObservationTruncated, UsageObservationUnsupportedEncoding,
		UsageObservationLimitExceeded:
		return true
	}
	return false
}

// AllUsageObservations returns the closed set of valid usage-observation values.
func AllUsageObservations() []UsageObservation {
	return []UsageObservation{
		UsageObservationNotApplicable,
		UsageObservationAbsent,
		UsageObservationComplete,
		UsageObservationMalformed,
		UsageObservationTruncated,
		UsageObservationUnsupportedEncoding,
		UsageObservationLimitExceeded,
	}
}
