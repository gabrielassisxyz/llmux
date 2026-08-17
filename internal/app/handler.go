package app

import (
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
	"github.com/gabrielassisxyz/llmux/internal/rewrite"
)

// HandlerDeps carries the collaborators the request chain needs. It is the
// single place the chain's dependencies are named, so a caller cannot
// assemble the chain with a missing piece.
type HandlerDeps struct {
	Generator      idgen.Generator
	AuthDigest     [32]byte
	Gate           *resource.Gate
	Clock          clock.Clock
	AdmissionStore resource.AdmissionStore
	UnroutedWriter rewrite.UnroutedRequestWriter
}

// BuildHandler composes the request path in the decided identity order and
// returns the handler the server serves. The route guard (path, method,
// query string) answers anonymously; everything past it carries an identity.
func BuildHandler(deps HandlerDeps, handlers proxy.Handlers) http.Handler {
	chat := rewrite.RequireIdentityEncoding(deps.UnroutedWriter,
		resource.RequireResources(deps.Gate, deps.AdmissionStore, deps.Clock,
			rewrite.RequireBoundedBody(deps.UnroutedWriter,
				rewrite.RequireScannedEnvelope(deps.UnroutedWriter, handlers.ChatCompletions))))

	return proxy.NewRouter(proxy.Handlers{
		Models:          withIdentity(deps, handlers.Models),
		ChatCompletions: withIdentity(deps, chat),
	})
}

// withIdentity wraps a route handler with identifier assignment, the logical
// deadline, and bearer authentication, in that order. It is the shared
// identity boundary every route past the route guard crosses.
func withIdentity(deps HandlerDeps, next http.HandlerFunc) http.HandlerFunc {
	return proxy.AssignRequestID(deps.Generator,
		proxy.RequireLogicalDeadline(
			proxy.RequireBearerAuth(deps.AuthDigest, next)))
}
