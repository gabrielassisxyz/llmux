// Account-health conformance matrix.
//
// Account health is one of three state machines this project expresses as a
// single table. Every event the proxy can classify has exactly one row, and
// both the unit tests in this package and the full-handler tests in
// internal/app read this table rather than encoding the rules a second time.
// A row that changes changes both test levels at once.
package route

import "fmt"

// HealthEvent names one row of the account-health conformance matrix. Each
// value is an upstream observation or a lifecycle boundary the proxy maps to
// exactly one health transition.
type HealthEvent int

const (
	// HealthEvent401 is an upstream 401, the credential-rejected answer.
	HealthEvent401 HealthEvent = iota
	// HealthEvent429FirstOrSecond is the first or second 429 within the
	// rolling 60-second window.
	HealthEvent429FirstOrSecond
	// HealthEvent429Third is the third 429 within the rolling window, the
	// count that opens the cooldown circuit.
	HealthEvent429Third
	// HealthEventCooldownGateExpiry is the gate deadline that the cooldown
	// threshold opened passing.
	HealthEventCooldownGateExpiry
	// HealthEventSingle429GateExpiry is the gate deadline that a single 429
	// opened passing.
	HealthEventSingle429GateExpiry
	// HealthEvent403 is an upstream 403.
	HealthEvent403
	// HealthEvent504 is an upstream 504.
	HealthEvent504
	// HealthEventOther5xxOr408 is an upstream 5xx other than 504, or a 408.
	HealthEventOther5xxOr408
	// HealthEventOtherNonAuth4xx is an upstream 4xx other than 401, 403 and
	// 408.
	HealthEventOtherNonAuth4xx
	// HealthEvent3xxOr101 is an upstream redirect or upgrade.
	HealthEvent3xxOr101
	// HealthEventTransportFailure is a dial or TLS timeout or a read failure
	// before commitment.
	HealthEventTransportFailure
	// HealthEventClientCancellation is a client cancellation or disconnect.
	HealthEventClientCancellation
	// HealthEventLogicalDeadlineExpiry is the logical request deadline passing.
	HealthEventLogicalDeadlineExpiry
	// HealthEventProcessRestart is a process restart, which reconstructs all
	// three accounts as enabled.
	HealthEventProcessRestart
)

// healthEventNames holds one stable, readable name per event, in declaration
// order. String indexes it, so the two must stay aligned.
var healthEventNames = [14]string{
	"upstream-401",
	"upstream-429-first-or-second",
	"upstream-429-third",
	"cooldown-gate-expiry",
	"single-429-gate-expiry",
	"upstream-403",
	"upstream-504",
	"upstream-other-5xx-or-408",
	"upstream-other-non-auth-4xx",
	"upstream-3xx-or-101",
	"transport-failure",
	"client-cancellation",
	"logical-deadline-expiry",
	"process-restart",
}

// String returns the event's name for test and log output.
func (e HealthEvent) String() string {
	if e >= 0 && int(e) < len(healthEventNames) {
		return healthEventNames[e]
	}
	return fmt.Sprintf("HealthEvent(%d)", int(e))
}

// HealthEffect is the health state an event leaves an account in.
// HealthUnchanged means the event leaves the state exactly as it was, which
// is the case for every row whose event carries no admission mutation.
type HealthEffect int

const (
	HealthUnchanged HealthEffect = iota
	HealthBecomesEnabled
	HealthBecomesCoolingDown
	HealthBecomesDisabled
)

// GateDeadlineEffect is what an event does to the account's rate gate
// deadline, the monotonic instant before which the account is not eligible.
type GateDeadlineEffect int

const (
	GateUntouched GateDeadlineEffect = iota
	// GateAdvanced is a 429 advancing the deadline by the derived delay,
	// clamped to ten minutes, never shortening an existing gate.
	GateAdvanced
	// GateAdvancedFloored is a third 429 advancing the deadline and then
	// flooring it at one full rolling window out.
	GateAdvancedFloored
	// GateCleared is a gate expiry clearing the deadline to zero.
	GateCleared
)

// HistoryEffect is what an event does to the account's recent-429 history.
type HistoryEffect int

const (
	HistoryUntouched HistoryEffect = iota
	// HistoryAppend is a 429 adding its receipt instant and pruning entries
	// outside the rolling window.
	HistoryAppend
	// HistoryCleared is a cooldown gate expiry clearing the history.
	HistoryCleared
)

// Mutation names the coordinator method that applies an event, or MutationNone
// when the event carries no admission mutation. The unit tests drive every
// non-none row through the named method; the none rows are relay-side and are
// exercised at the full-handler level.
type Mutation int

const (
	MutationNone Mutation = iota
	MutationDisable
	MutationApply429
	MutationExpireGateIfDue
	MutationRestart
)

// HealthRow is one row of the account-health conformance matrix: the health,
// gate-deadline and recent-429-history effects one event has on an account,
// the coordinator mutation that applies it, and the relay-side effects only
// the full-handler level can prove.
type HealthRow struct {
	Event    HealthEvent
	Health   HealthEffect
	Gate     GateDeadlineEffect
	History  HistoryEffect
	Mutation Mutation

	// Effects is the "Other required effects" column in prose. It records
	// the coordinator-observable and relay-side effects that are not one of
	// the three state columns, so the matrix carries every row the design
	// specifies. The unit level asserts the state columns; the full-handler
	// level asserts the relay-side effects.
	Effects string
}

// HealthMatrix is the account-health conformance matrix. It is the single
// source of truth for every health transition: the unit tests and the
// full-handler tests both read this slice rather than restating any row. It
// is read-only data; tests never mutate it.
var HealthMatrix = []HealthRow{
	{
		Event:    HealthEvent401,
		Health:   HealthBecomesDisabled,
		Gate:     GateUntouched,
		History:  HistoryUntouched,
		Mutation: MutationDisable,
		Effects:  "remove session pins to the account, wake waiters, fail over to another eligible account with no backoff, never relay the 401, its body, or its WWW-Authenticate",
	},
	{
		Event:    HealthEvent429FirstOrSecond,
		Health:   HealthUnchanged,
		Gate:     GateAdvanced,
		History:  HistoryAppend,
		Mutation: MutationApply429,
		Effects:  "retry within budget; a base route prefers a different account immediately; explicit aliases stay on their named account",
	},
	{
		Event:    HealthEvent429Third,
		Health:   HealthBecomesCoolingDown,
		Gate:     GateAdvancedFloored,
		History:  HistoryAppend,
		Mutation: MutationApply429,
		Effects:  "wake waiters",
	},
	{
		Event:    HealthEventCooldownGateExpiry,
		Health:   HealthBecomesEnabled,
		Gate:     GateCleared,
		History:  HistoryCleared,
		Mutation: MutationExpireGateIfDue,
		Effects:  "lazy; wake waiters",
	},
	{
		Event:    HealthEventSingle429GateExpiry,
		Health:   HealthUnchanged,
		Gate:     GateCleared,
		History:  HistoryUntouched,
		Mutation: MutationExpireGateIfDue,
		Effects:  "lazy; clearing here would reset the count on the first 429 and the threshold could never be reached",
	},
	{
		Event:   HealthEvent403,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "non-retryable; relayed unchanged",
	},
	{
		Event:   HealthEvent504,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "retry prefers a different eligible account",
	},
	{
		Event:   HealthEventOther5xxOr408,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "a valid Retry-After becomes the minimum delay for a retry that targets the same account, is stored unclamped, and gates nothing else",
	},
	{
		Event:   HealthEventOtherNonAuth4xx,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "non-retryable; relayed unchanged",
	},
	{
		Event:   HealthEvent3xxOr101,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "local 502 before commitment; non-retryable",
	},
	{
		Event:   HealthEventTransportFailure,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "retry within its class budget",
	},
	{
		Event:   HealthEventClientCancellation,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "none",
	},
	{
		Event:   HealthEventLogicalDeadlineExpiry,
		Health:  HealthUnchanged,
		Gate:    GateUntouched,
		History: HistoryUntouched,
		Effects: "terminal; never itself retried",
	},
	{
		Event:    HealthEventProcessRestart,
		Health:   HealthBecomesEnabled,
		Gate:     GateCleared,
		History:  HistoryCleared,
		Mutation: MutationRestart,
		Effects:  "no probe of any kind; disabled state is deliberately not restored",
	},
}
