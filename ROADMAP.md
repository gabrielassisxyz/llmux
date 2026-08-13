# Roadmap

Direction, for whoever arrives. The detailed implementation contract is
`docs/plans/PLAN.md`; this file is the short answer to "what works, what doesn't, and
what is never going to be here".

## What exists

The development harness only. `bin/ci`, the versioned git hooks, the worktree tool, the
CI workflow and the spec are in place; no proxy code has been written yet.

## What is missing

The implementation, in the sequence the plan fixes:

1. **Project skeleton and fixed route catalog** — module, configuration loading and
   validation, the route table, the generated per-account variants, and the
   deterministic `/v1/models` projection.
2. **The attempt store** — SQLite, append-only, one row per attempt, with startup
   recovery.
3. **Request scanner and rewriter** — route-owned parameter injection, and nothing else
   that touches the body.
4. **The account coordinator** — per-account rolling-window admission, in-flight
   ceilings, session affinity and leases.
5. **Upstream execution and retry** — retry budgets split by error class, and account
   health that drops a revoked key on its first failure.
6. **Relay and usage observation** — streaming and non-streaming, time to first token,
   and token counts read without buffering the response.
7. **Lifecycle and hardening** — graceful shutdown, deadlines, bounded buffering.
8. **Consumer acceptance** — the three existing clients cut over to it.

Gates the plan requires that `bin/ci` does not run yet, because they describe code that
does not exist: fuzz smoke runs for the body and usage parsers, the integration-tagged
suite, a check that the built Linux binary has no dynamic runtime dependency, and the
three searches proving the absence of prompt logging, cost computation and any
background upstream probe loop. Each is wired in the phase that gives it something to
check.

Undecided: the cutover shape. Either run on a second port and migrate consumers one at
a time, or replace the existing endpoint in place.

## Deliberately out of scope

These are not "not yet". They are decisions, and each one has a reason recorded in
`docs/plans/PLAN.md`:

- **A second provider dialect, or any translation between API shapes.** Every upstream
  here is one shape. Translating between shapes is what broke the proxy this replaces:
  a field renamed on the way down with no inverse on the way up, which silently
  degraded the model's output rather than failing.
- **Parameter validation.** The proxy forwards; it does not adjudicate what the
  upstream accepts.
- **Cost accounting.** The upstream is flat-rate and never reports how much of the
  input was served from cache, so any monetary figure would be a ceiling presented as a
  measurement. Token counts are logged; pricing is applied downstream.
- **Prompt and completion storage.** Token counts only.
- **Background health checks.** Polling every deployment on a timer costs more requests
  than the traffic it protects.
- **Tenancy, a user model, a web UI, a plugin system, and a runtime-configurable
  backend list.** One operator, one machine, one shared key, a route catalog fixed in
  source.
- **A container, a runtime, or an external database.** One static binary and one
  embedded single-file store.
