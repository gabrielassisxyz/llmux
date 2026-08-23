package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/errcode"
)

// ErrorCode is re-exported from internal/errcode so the error envelope and
// the packages that answer it keep one vocabulary while internal/route can
// name the same codes without importing this package.
type ErrorCode = errcode.ErrorCode

const (
	ErrInvalidAPIKey              = errcode.ErrInvalidAPIKey
	ErrMethodNotAllowed           = errcode.ErrMethodNotAllowed
	ErrNotFound                   = errcode.ErrNotFound
	ErrQueryNotSupported          = errcode.ErrQueryNotSupported
	ErrInvalidSessionHeader       = errcode.ErrInvalidSessionHeader
	ErrRequestTooLarge            = errcode.ErrRequestTooLarge
	ErrUnsupportedContentEncoding = errcode.ErrUnsupportedContentEncoding
	ErrInvalidRequest             = errcode.ErrInvalidRequest
	ErrJSONDepthExceeded          = errcode.ErrJSONDepthExceeded
	ErrModelNotFound              = errcode.ErrModelNotFound
	ErrProxyOverloaded            = errcode.ErrProxyOverloaded
	ErrAccountCapacityTimeout     = errcode.ErrAccountCapacityTimeout
	ErrAdmissionStoreUnavailable  = errcode.ErrAdmissionStoreUnavailable
	ErrAccountUnavailable         = errcode.ErrAccountUnavailable
	ErrUpstreamAuthFailure        = errcode.ErrUpstreamAuthFailure
	ErrUpstreamUnavailable        = errcode.ErrUpstreamUnavailable
	ErrInvalidUpstreamResponse    = errcode.ErrInvalidUpstreamResponse
	ErrDeadlineExceeded           = errcode.ErrDeadlineExceeded
	ErrInternalError              = errcode.ErrInternalError
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

	retryAfter := errcode.CalculateRetryAfter(reopen, now)
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
