package proxy

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"
)

type ErrorCode string

const (
	ErrInvalidAPIKey              ErrorCode = "invalid_api_key"
	ErrMethodNotAllowed           ErrorCode = "method_not_allowed"
	ErrNotFound                   ErrorCode = "not_found"
	ErrQueryNotSupported          ErrorCode = "query_not_supported"
	ErrInvalidSessionHeader       ErrorCode = "invalid_session_header"
	ErrRequestTooLarge            ErrorCode = "request_too_large"
	ErrUnsupportedContentEncoding ErrorCode = "unsupported_content_encoding"
	ErrInvalidRequest             ErrorCode = "invalid_request"
	ErrJSONDepthExceeded          ErrorCode = "json_depth_exceeded"
	ErrModelNotFound              ErrorCode = "model_not_found"
	ErrProxyOverloaded            ErrorCode = "proxy_overloaded"
	ErrAccountCapacityTimeout     ErrorCode = "account_capacity_timeout"
	ErrAdmissionStoreUnavailable  ErrorCode = "admission_store_unavailable"
	ErrAccountUnavailable         ErrorCode = "account_unavailable"
	ErrUpstreamAuthFailure        ErrorCode = "upstream_auth_failure"
	ErrUpstreamUnavailable        ErrorCode = "upstream_unavailable"
	ErrInvalidUpstreamResponse    ErrorCode = "invalid_upstream_response"
	ErrDeadlineExceeded           ErrorCode = "deadline_exceeded"
	ErrInternalError              ErrorCode = "internal_error"
)

type ErrorType string

const (
	TypeAuthenticationError ErrorType = "authentication_error"
	TypeInvalidRequestError ErrorType = "invalid_request_error"
	TypeRateLimitError      ErrorType = "rate_limit_error"
	TypeServerError         ErrorType = "server_error"
)

type ErrorEnvelope struct {
	Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
	Message string    `json:"message"`
	Type    ErrorType `json:"type"`
	Param   *string   `json:"param,omitempty"`
	Code    ErrorCode `json:"code"`
}

type LocalError struct {
	Code    ErrorCode
	Type    ErrorType
	Status  int
	Message string
}

var errorsByCode = map[ErrorCode]LocalError{
	ErrInvalidAPIKey:              {ErrInvalidAPIKey, TypeAuthenticationError, 401, "Bad proxy key"},
	ErrMethodNotAllowed:           {ErrMethodNotAllowed, TypeInvalidRequestError, 405, "Unsupported method"},
	ErrNotFound:                   {ErrNotFound, TypeInvalidRequestError, 404, "Unknown path"},
	ErrQueryNotSupported:          {ErrQueryNotSupported, TypeInvalidRequestError, 400, "Non-empty query string"},
	ErrInvalidSessionHeader:       {ErrInvalidSessionHeader, TypeInvalidRequestError, 400, "Invalid session header"},
	ErrRequestTooLarge:            {ErrRequestTooLarge, TypeInvalidRequestError, 413, "Body too large"},
	ErrUnsupportedContentEncoding: {ErrUnsupportedContentEncoding, TypeInvalidRequestError, 415, "Compressed body"},
	ErrInvalidRequest:             {ErrInvalidRequest, TypeInvalidRequestError, 400, "Invalid routing envelope"},
	ErrJSONDepthExceeded:          {ErrJSONDepthExceeded, TypeInvalidRequestError, 400, "Nesting depth exceeded"},
	ErrModelNotFound:              {ErrModelNotFound, TypeInvalidRequestError, 404, "Unknown alias"},
	ErrProxyOverloaded:            {ErrProxyOverloaded, TypeRateLimitError, 429, "Global request or memory overload"},
	ErrAccountCapacityTimeout:     {ErrAccountCapacityTimeout, TypeRateLimitError, 429, "Temporary account-capacity timeout"},
	ErrAdmissionStoreUnavailable:  {ErrAdmissionStoreUnavailable, TypeServerError, 503, "Dispatch-admission store unavailable"},
	ErrAccountUnavailable:         {ErrAccountUnavailable, TypeServerError, 503, "Account disabled"},
	ErrUpstreamAuthFailure:        {ErrUpstreamAuthFailure, TypeServerError, 502, "Upstream credential rejected with no eligible account left"},
	ErrUpstreamUnavailable:        {ErrUpstreamUnavailable, TypeServerError, 502, "Exhausted transport failure"},
	ErrInvalidUpstreamResponse:    {ErrInvalidUpstreamResponse, TypeServerError, 502, "Unexpected upstream redirect or upgrade"},
	ErrDeadlineExceeded:           {ErrDeadlineExceeded, TypeServerError, 504, "Overall timeout before commit"},
	ErrInternalError:              {ErrInternalError, TypeServerError, 500, "Recovered panic before commit"},
}

// ErrorStatus returns the HTTP status for a known local error code, or 500
// if the code is not part of the fixed vocabulary. It lets durable row
// writers derive downstream_status from the same source of truth that answers
// the client.
func ErrorStatus(code ErrorCode) int {
	if def, ok := errorsByCode[code]; ok {
		return def.Status
	}
	return http.StatusInternalServerError
}

// UnroutedRequestWriter records a local rejection as one unrouted_request
// row. It names ErrorCode and therefore lives here: a middleware that
// records a rejection, in this package or in rewrite, holds this type
// without either package importing the other in a cycle.
type UnroutedRequestWriter interface {
	RecordUnroutedRequest(context.Context, ErrorCode) error
}

// WriteError writes the OpenAI-shaped error envelope to the response.
func WriteError(w http.ResponseWriter, reqID string, code ErrorCode) {
	errDef, ok := errorsByCode[code]
	if !ok {
		errDef = errorsByCode[ErrInternalError]
	}

	w.Header().Set("Content-Type", "application/json")
	if reqID != "" {
		w.Header().Set("X-LLMux-Request-ID", reqID)
	}

	w.WriteHeader(errDef.Status)

	env := ErrorEnvelope{
		Error: ErrorDetails{
			Message: errDef.Message,
			Type:    errDef.Type,
			Code:    errDef.Code,
		},
	}
	_ = json.NewEncoder(w).Encode(env)
}

// CalculateRetryAfter returns the Retry-After header value in whole seconds, rounded up.
// It is never less than 1.
func CalculateRetryAfter(reopen time.Time, now time.Time) int {
	if reopen.IsZero() || reopen.Before(now) {
		return 1
	}
	diff := reopen.Sub(now).Seconds()
	sec := int(math.Ceil(diff))
	if sec < 1 {
		return 1
	}
	return sec
}

// WriteRateLimitError writes a 429 error and includes the Retry-After header.
func WriteRateLimitError(w http.ResponseWriter, reqID string, code ErrorCode, reopen time.Time, now time.Time) {
	errDef, ok := errorsByCode[code]
	if !ok || errDef.Type != TypeRateLimitError {
		errDef = errorsByCode[ErrProxyOverloaded]
	}

	w.Header().Set("Content-Type", "application/json")
	if reqID != "" {
		w.Header().Set("X-LLMux-Request-ID", reqID)
	}

	retryAfter := CalculateRetryAfter(reopen, now)
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	w.WriteHeader(errDef.Status)

	env := ErrorEnvelope{
		Error: ErrorDetails{
			Message: errDef.Message,
			Type:    errDef.Type,
			Code:    errDef.Code,
		},
	}
	_ = json.NewEncoder(w).Encode(env)
}
