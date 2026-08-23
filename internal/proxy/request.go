package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"sort"
)

// upstreamChatCompletionsURL is the fixed upstream chat-completions
// endpoint. It is a source constant: no request-time input can change it.
const upstreamChatCompletionsURL = UpstreamBaseURL + "/chat/completions"

// BuildUpstreamRequest builds the outbound *http.Request template for one
// dispatch attempt. It copies only the fixed end-to-end allowlist from
// clientReq, sets the derived headers, and reports the number of dropped
// headers through droppedHeaderCount.
//
// body and contentLength are the replayed request body and its exact
// length, derived from the rewrite plan (rewrite.Plan.Reader and
// rewrite.Plan.ContentLength). The allowlist is Content-Type, Accept,
// Accept-Encoding and User-Agent, each copied only when the client sent
// it. Every other client header is dropped: its name is counted in
// droppedHeaderCount and named, never valued, in a debug event. A client
// that sent no User-Agent gets an explicitly empty one so the transport
// does not synthesize its own default.
//
// The template carries no Authorization header: the account credential is
// added by SetAccountAuthorization only after account selection, so the
// template can be built and validated before any account capacity is
// reserved. The outbound request carries no GetBody and no idempotency
// header, so the transport cannot transparently replay the POST. logger
// must be non-nil; it receives the debug event naming dropped headers.
func BuildUpstreamRequest(
	clientReq *http.Request,
	body io.Reader,
	contentLength int64,
	logger *slog.Logger,
	droppedHeaderCount *int64,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(clientReq.Context(), http.MethodPost, upstreamChatCompletionsURL, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = contentLength

	var dropped []string
	for name, values := range clientReq.Header {
		if allowlistedHeader(name) {
			req.Header[name] = append([]string(nil), values...)
		} else {
			dropped = append(dropped, name)
		}
	}

	// Force an empty User-Agent when the client sent none, so the
	// transport does not synthesize its own default.
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header.Set("User-Agent", "")
	}

	if droppedHeaderCount != nil {
		*droppedHeaderCount = int64(len(dropped))
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		logger.Debug("request headers dropped by allowlist", "count", len(dropped), "names", dropped)
	}

	return req, nil
}

// SetAccountAuthorization installs the selected account's bearer
// credential on the outbound request. It is the one derived header that
// cannot be part of the template, because the account is not known until
// selection completes.
func SetAccountAuthorization(req *http.Request, accountKey string) {
	req.Header.Set("Authorization", "Bearer "+accountKey)
}

// allowlistedHeader reports whether name is one of the fixed end-to-end
// request headers the proxy forwards.
func allowlistedHeader(name string) bool {
	switch name {
	case "Content-Type", "Accept", "Accept-Encoding", "User-Agent":
		return true
	}
	return false
}
