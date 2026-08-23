// Package errcode holds the fixed local-error vocabulary shared by the
// packages that answer the OpenAI error envelope and the packages that
// classify local failures. It is a leaf package so that internal/route,
// which performs no I/O, can name an error without importing
// internal/proxy, whose HTTP handlers the coordinator has no business
// depending on.
package errcode

// ErrorCode is a fixed local error code from the proxy's error vocabulary.
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
