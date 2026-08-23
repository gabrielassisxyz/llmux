package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/route"
	"github.com/gabrielassisxyz/llmux/internal/store"
)

// DispatchDeps carries the collaborators the dispatch sequence needs. It is
// the single place the sequence's dependencies are named, so a caller
// cannot assemble the sequence with a missing piece.
type DispatchDeps struct {
	Coordinator     *route.Coordinator
	AdmissionWriter store.AdmissionWriter
	Generator       idgen.Generator
	Client          *http.Client
	Clock           clock.Clock
	Logger          *slog.Logger
	AccountKeys     route.AccountKeys
}

// DispatchError is a local failure in the dispatch sequence, carrying the
// local error code the caller answers with.
type DispatchError struct {
	Code ErrorCode
}

func (e *DispatchError) Error() string {
	return "dispatch failed: " + string(e.Code)
}

// DispatchResult is the outcome of one dispatch attempt.
type DispatchResult struct {
	// Response is the upstream response, non-nil only when the dispatch
	// succeeded.
	Response *http.Response

	// Lease is the finalized lease, non-nil only when the dispatch
	// succeeded. The caller releases it when the response body closes.
	Lease *route.PendingLease

	// Skips are the selection skip decisions collected during the
	// selection phase, for the terminal row.
	Skips []route.SkipDecision

	// DroppedHeaderCount is the number of request headers the allowlist
	// removed when the template was built, for the terminal row.
	DroppedHeaderCount int64

	// Failure is the classified selection failure, non-nil only when the
	// selection phase ended without a lease.
	Failure *route.SelectionFailure

	// Err is the failure for the admission commit or the transport call,
	// non-nil only when the dispatch failed after selection succeeded.
	Err error
}

// Dispatch performs one dispatch attempt following the fixed sequence:
// build the request template, select an account, commit the admission row,
// finalize the lease, and call http.Client.Do. The admission row is
// committed before Do with no exception, and a failed admission commit
// releases the lease and answers local 503 admission_store_unavailable.
func Dispatch(
	ctx context.Context,
	deps DispatchDeps,
	clientReq *http.Request,
	body io.Reader,
	contentLength int64,
	entry catalog.Route,
	logicalRequestID string,
	attemptNo int,
) *DispatchResult {
	// Build the template before reserving any account capacity, so the
	// admission commit is the only fallible step left between reservation
	// and Do.
	var droppedCount int64
	req, err := BuildUpstreamRequest(clientReq, body, contentLength, deps.Logger, &droppedCount)
	if err != nil {
		return &DispatchResult{Err: err}
	}

	// Select an account. A pinned alias names one account and never
	// spills; a base alias considers all three in permutation order.
	var selection route.SelectionResult
	if len(entry.EligibleAccounts) == 1 {
		selection = deps.Coordinator.SelectExplicit(ctx, entry.EligibleAccounts[0])
	} else {
		selection = deps.Coordinator.Select(ctx)
	}
	if selection.Lease == nil {
		failure := deps.Coordinator.ClassifySelectionFailure(selection, ctx.Err())
		return &DispatchResult{Skips: selection.Skips, Failure: &failure}
	}
	lease := selection.Lease

	reservedAtUS := deps.Clock.WallNow().UnixMicro()

	attemptID, err := deps.Generator.AttemptID()
	if err != nil {
		lease.Release()
		return &DispatchResult{Skips: selection.Skips, Err: err}
	}

	admission := store.DispatchAdmission{
		AttemptID:        attemptID,
		LogicalRequestID: logicalRequestID,
		AttemptNo:        attemptNo,
		AccountLabel:     string(lease.Account()),
		RequestedAlias:   entry.Alias,
		UpstreamModel:    entry.UpstreamModel,
		ReservedAtUS:     reservedAtUS,
		LimiterRPMUsed:   lease.RateWindowAtReserve(),
		LimiterInFlight:  lease.InFlightAtReserve(),
	}
	if err := deps.AdmissionWriter.InsertDispatchAdmission(ctx, admission); err != nil {
		lease.Release()
		return &DispatchResult{Skips: selection.Skips, Err: &DispatchError{Code: ErrAdmissionStoreUnavailable}}
	}

	// Finalize the reservation into the rolling window, then dispatch. The
	// credential is the one derived header the template could not carry,
	// because the account is not known until selection completes.
	lease.Finalize()
	SetAccountAuthorization(req, accountKeyFor(deps.AccountKeys, lease.Account()))

	resp, err := deps.Client.Do(req)
	if err != nil {
		lease.Release()
		return &DispatchResult{Skips: selection.Skips, Err: err, DroppedHeaderCount: droppedCount}
	}

	return &DispatchResult{Response: resp, Lease: lease, Skips: selection.Skips, DroppedHeaderCount: droppedCount}
}

// accountKeyFor maps a fixed account identity to its credential.
func accountKeyFor(keys route.AccountKeys, account catalog.Account) string {
	switch account {
	case catalog.AccountK1:
		return keys.K1
	case catalog.AccountK2:
		return keys.K2
	default:
		return keys.K3
	}
}
