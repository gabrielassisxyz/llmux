# llmux Swarm Operating Manual

## Rule 0: Direct instructions override this manual

A direct human instruction overrides every rule below. Follow the instruction, record any lasting technical decision in the relevant bead and Agent Mail thread, then continue.

## Rule 1: Never delete files

Never delete a file or directory without explicit written permission naming the exact target. This includes files created during the current work.

If deletion appears necessary and permission is unavailable, leave the file in place and record the required follow-up in Beads. Do not substitute another destructive command that achieves the same result indirectly.

## Destructive commands are forbidden

The shared working tree contains work from multiple agents. A destructive command can erase changes that do not belong to the agent running it.

Never run any of these commands or an equivalent:

- `git reset --hard`
- `git reset --merge`
- `git checkout -- <path>`
- `git restore <path>` when it changes the working tree
- `git clean -f`, `git clean -fd`, or `git clean -fdx`
- `git push --force`, `git push -f`, or `git push --force-with-lease`
- `git branch -D`
- `git stash drop` or `git stash clear`
- `rm -rf`, recursive deletion through `find`, or equivalent filesystem removal
- Any command that overwrites uncommitted work, rewrites shared history, or deletes untracked files

Do not use `git stash`, including automatic stashing. A stash operates on the shared working tree and can capture, hide, or restore another agent's changes.

Safe inspection commands include `git status`, `git diff`, `git diff --cached`, `git log`, `git show`, and `git fetch`.

`git restore --staged <path>` may be used only to unstage a path staged by the same agent. Never unstage another agent's work.

DCG is installed and mechanically blocks known destructive commands. Do not seek a bypass code, disable its hook, split a blocked command into another form, or ask another agent to run it. The command was blocked because instructions alone are not sufficient protection.

```bash
dcg test "<command>" --explain
dcg doctor --format json
```

DCG is one of three coupled protections required by the single-branch model. File reservations and the Agent Mail pre-commit guard are the other two. If any protection is unavailable, do not implement or commit code until it is restored.

## What llmux is

llmux is an OpenAI-compatible routing proxy. It fronts three Ollama Cloud account keys behind one local endpoint, resolves a short model alias to an upstream model plus an account, forwards the request unaltered, and records one row per upstream attempt.

## Calibration

Tier 2, current engineering state: make it work. External stakes are contained by one local deployment, one authority boundary, and no tenancy. Reliability stakes are high because llmux sits on the critical path of three consumers, and silent request corruption is the defect it exists to prevent. Move from make it work to make it right and then make it fast only when the bead graph opens the corresponding gate.

## What doing a good job means

A good change implements the exact bead contract, preserves every proxy invariant, is born with tests, remains race-free, coordinates its files before editing, leaves `main` green, pushes immediately, exposes no credential or private context, and leaves enough durable state in Beads and Agent Mail for any generalist agent to continue without reconstruction.

Fast code that weakens an invariant is wrong. Correct code stranded in an unpushed commit is unfinished. Useful work that is not represented in the bead graph is invisible to the swarm.

## Read this file completely

Read this entire file before taking work. Do not skim only the command tables.

After every context compaction, reread this entire file without waiting to be prompted. Then reread the active bead, its Agent Mail thread, the current diff, and the current reservation state before resuming.

Beads carry implementation context. Read `docs/plans/PLAN.md` only when a bead explicitly directs it or when the bead is demonstrably missing information. Do not replace bead execution with a fresh interpretation of the full plan.

## First-minute protocol

1. Confirm the repository root and inspect `git status --short --branch`.
2. Confirm the checked-out branch is `main`. Do not create or switch to a feature branch or worktree.
3. Read this file, `ROADMAP.md`, and the bead selected for inspection.
4. Register through MCP Agent Mail with `macro_start_session`.
5. Export the returned Agent Mail identity as `AGENT_NAME`.
6. Fetch and acknowledge relevant inbox messages.
7. Run `bv --robot-triage`.
8. Run `br ready --json`.
9. Select only a ready bead supported by the graph recommendation.
10. Read it with `br show <id> --json`.
11. Claim it with `br update <id> --status in_progress --json`.
12. Reserve the exact edit surface with `file_reservation_paths`.
13. Inspect reservation conflicts. Do not edit a conflicting path.
14. Announce the claim in the bead's Agent Mail thread.
15. Establish the relevant test baseline, then implement.

Do not spend the opening of the work cycle broadcasting status without claiming work. Coordination exists to make implementation safe, not to replace implementation.

## Stack and commands

| Item | Value |
|---|---|
| Language | Go 1.26.2 |
| Dependency model | Go modules |
| HTTP server | `net/http` |
| Upstream client | `net/http` |
| Architecture | Standard library first |
| Web framework | None |
| Router dependency | None |
| ORM | None |
| Deployment shape | One static binary |
| Full local gate | `bin/ci` |
| Beads | `br` 0.2.19 |
| Graph triage | `bv` v0.16.1 |
| Multiplexer | `ntm` v1.22.1 |
| Coordination | MCP Agent Mail |
| Destructive guard | DCG |
| Remote compilation | Not installed and not wanted |
| UBS | Not installed, optional only |

```bash
go build ./cmd/llmux
go run ./cmd/llmux
go test ./...
go test -race ./...
go vet ./...
bin/ci
```

Run `bin/install-hooks` once per clone. Then install and verify the Agent Mail reservation guard as described in the Git safety section.

Do not introduce RCH. It exists to offload expensive Rust compilation. Go builds are cheap enough to run locally, and adding remote compilation would create infrastructure without a present need.

UBS is optional if it becomes available. It is never a required gate and never replaces `bin/ci`, `go vet`, the race detector, self-review, or cross-agent review.

## Scope

The current scope is one OpenAI-shaped route over one upstream API shape, for the three consumers that speak to it today. Do not expand beyond it without a present need. If a bead appears to require broader product scope, stop implementation and record the conflict in the bead and its thread.

The following are explicitly out of scope:

- A second provider dialect
- A request or response translation layer
- Tenancy
- A user model
- A web UI
- A plugin surface
- A runtime-configurable backend list
- A framework or general proxy platform
- A container requirement
- Cost calculation
- Background health probing

The route catalog is fixed in source. A router over one API shape is the entire design.

## Proxy invariants

These are not preferences. Each invariant prevents a measured defect from the proxy llmux replaces. Reintroducing one recreates the failure that motivated the project. The detailed contract lives in `docs/plans/PLAN.md` sections 4 and 5.

### Never touch the message array

The conversation crosses the proxy byte for byte. The prior failure was not a deletion. It was a one-way field rename with no inverse on the return path, which made the model stop reasoning and stop converging.

A proxy that translates a field name owes the inverse translation. llmux routes one shape and therefore must not incur that debt. Route-owned parameter injection is the only permitted request mutation.

Tests for routing must prove byte preservation, not merely semantic JSON equivalence.

### Do not validate forwarded parameters

Forward what the client sent. Do not decide which parameters the upstream should accept.

Rejecting a parameter documented by the upstream as valid is how the prior proxy broke working requests. llmux may validate the minimal routing envelope it owns, but it must not become a policy engine for upstream parameters.

### Count rate limits per account

An upstream account is the resource with a real ceiling. A deployment name, alias, or model label is not.

Per-deployment counters let one conversation reach the same account through multiple names and send more traffic than the configured account limit. Every rolling window and failure decision must therefore be keyed by account identity.

### Never key records on an upstream-generated identifier

Upstream response identifiers repeat within a small space. Using one as a primary key lost 83 percent of attempt rows.

Every upstream attempt needs a locally controlled unique identity. Upstream identifiers are data, never storage identity.

### Do not run background health checks

Timer-driven probes across every route were measured in tens of thousands of billed requests per day.

Retries, per-account failure state, and dropping a bad key when a real request proves it bad provide the required availability behavior without synthetic traffic.

### Log token counts, never prompts or completions

Attempt records may contain token counts and operational metadata. They must never contain request messages, prompts, completion text, streamed content, or reconstructed conversation fragments.

Observability does not justify retaining content.

### Never compute cost

The upstream is flat-rate and does not report the cached share. Any computed cost would be a ceiling presented as a measurement.

Record token counts. Pricing, if ever needed, belongs downstream where assumptions can be explicit.

## The initial bead frontier

The graph contains 152 beads: 149 open and 3 deferred. Of those, 144 are blocked. `bv` reports 8 actionable items, while `br ready --json` returns 3 ready items. `bv --robot-plan` computes four tracks with sizes 1, 1, 5, and 1.

The gate epics from Phase 0 through Phase 7 are strict. Do not skip a gate to reach an attractive downstream bead. The current graph top pick is the root epic itself.

The frontier is intentionally narrow. It widens when each gate closes. Start with no more implementation agents than there are distinct ready beads with non-overlapping edit surfaces. The initial swarm is small because the dependency graph permits little safe parallel work, not because small swarms are preferred in general.

The live commands are authoritative when counts change. The snapshot above explains the bootstrap posture; it does not authorize work that `br ready --json` no longer returns.

A deferred bead is not ready work. A `bv` execution track containing a deferred item does not override its status.

## Coordination model

The swarm uses one shared working tree and one shared `main` branch. Coordination is distributed across durable artifacts and tools.

| Layer | Authority |
|---|---|
| Beads | Task state, priority, dependencies, readiness, acceptance criteria |
| bv | Graph analysis and the best next work |
| MCP Agent Mail | Identity, targeted communication, threads, acknowledgments, file reservations |
| Git | Integrated source history on `main` |
| `bin/ci` | Repository quality gate |
| DCG | Mechanical rejection of destructive commands |

No layer replaces another. Beads without bv produces arbitrary task selection. bv without Beads has no durable task authority. Beads and bv without Agent Mail allow agents to collide. Agent Mail without the pre-commit guard leaves reservations as advice only. The single-branch model is safe only when reservations, the reservation guard, and DCG are all active together.

## Beads with `br`

Beads is the task authority. Do not maintain implementation work in ad hoc Markdown checklists, Agent Mail messages, source comments, or memory when it belongs in the graph.

### Command reference

| Command | Purpose |
|---|---|
| `br ready --json` | List unblocked work |
| `br list --status open --json` | List open work |
| `br show <id> --json` | Read the full bead and dependencies |
| `br update <id> --status in_progress --json` | Claim work |
| `br close <id> --reason "Completed" --json` | Close completed work |
| `br create --title "..." --type task --priority 2` | Create discovered work |
| `br dep add <id> <dependency-id>` | Add a dependency |
| `br comments add <id> "..."` | Record durable implementation context |
| `br sync --flush-only` | Flush tracker state without Git operations |

Priorities are numeric: P0 is critical, P1 is high, P2 is medium, P3 is low, and P4 is backlog. Use the existing bead type and label conventions.

### Private tracker topology

The tracker is deliberately not committed to llmux. `.beads` is a symlink into the Git-ignored `local/` directory, which points at a private maintainer-notes repository. The bead graph is versioned there.

Always run `br sync --flush-only` after changing bead state.

Never run `git add .beads/`. Never commit `.beads`, its symlink target, tracker JSONL, or private notes into llmux. Do not edit tracker files directly.

### Claiming rules

- **Readiness authority:** A bead must appear in `br ready --json` before implementation begins.
- **Graph authority:** Use `bv --robot-triage` to rank the ready set. Do not choose by title, familiarity, or apparent ease.
- **One claim:** Claim one implementation bead at a time.
- **Atomic claim:** Claim before reserving files. If another agent has already claimed it, select again.
- **Full context:** Read the complete bead, dependencies, acceptance criteria, and existing comments before editing.
- **No invented work:** If no bead is ready, do not start a blocked item or invent adjacent scope.
- **Discovered work:** Create a bead when a real defect or required follow-up is found. Link dependencies and explain why it is separate.
- **No silent expansion:** If discovered work is required for the current bead, update the bead or create and link a prerequisite before proceeding.
- **Close honestly:** Close only after implementation, self-review, all required tests, commit, and successful push.

If `br ready --json` is empty, check Agent Mail for requested cross-agent review, inspect blockers with robot-mode bv commands, or wait for a gate to close. Activity is not progress when the graph says the work is blocked.

## Graph triage with `bv`

bv decides what work has the highest graph impact. It does not claim work, reserve files, or communicate with other agents.

Use only `--robot-*` flags. Bare `bv` opens an interactive TUI and blocks the calling process.

```bash
bv --robot-triage
bv --robot-next
bv --robot-plan
bv --robot-insights
bv --robot-priority
bv --robot-diff --diff-since <ref>
bv --robot-alerts
bv --robot-suggest
```

### Required selection sequence

```text
bv --robot-triage
br ready --json
br show <selected-id> --json
br update <selected-id> --status in_progress --json
file_reservation_paths(...)
send_message(...)
implement
```

The recommendation must intersect the ready set. A high-scoring blocked bead remains blocked. A deferred track remains deferred.

Check the `status` fields in robot output. Metrics may be computed, approximate, skipped, or timed out. Do not present an unavailable metric as a result.

Use `bv --robot-plan` before increasing swarm size. Add agents only when the live plan exposes additional independent tracks and the ready beads have non-overlapping edit surfaces.

## MCP Agent Mail

MCP Agent Mail provides identity, inboxes, targeted messages, threaded coordination, acknowledgments, and advisory file reservations.

Use the repository root as the project key. Never publish a machine-specific absolute path in repository content.

### Register at startup

```text
macro_start_session(
  human_key="<absolute-repository-path>",
  program="<agent-program>",
  model="<model>",
  task_description="<bead-id>: <bead-title>"
)
```

Save the returned agent name and export it in the shell that will commit:

```bash
export AGENT_NAME="<returned-agent-name>"
```

The Git reservation guard uses `AGENT_NAME` to distinguish the current agent from reservation holders.

### Reserve before editing

```text
file_reservation_paths(
  project_key="<absolute-repository-path>",
  agent_name="<agent-name>",
  paths=["exact/path.go", "internal/package/*.go"],
  ttl_seconds=3600,
  exclusive=true,
  reason="<bead-id>"
)
```

Use the narrowest correct path set. Reserve concrete files when known. Use a package glob only when the bead genuinely needs the package surface.

Set a TTL long enough for the immediate edit, not for the whole day. Renew a reservation that is still active. Release it after the commit has been pushed.

A successful reservation call can still report conflicts. Inspect the conflict list. Do not edit a path reserved by another agent, even if the API returned a lease. Message the holder in the shared bead thread, narrow the paths, or select another bead.

### Thread every bead consistently

The bead ID is the anchor across all coordination layers.

| Layer | Convention |
|---|---|
| Mail `thread_id` | Exact bead ID |
| Mail subject | `[llmux-...] <event>: <short description>` |
| Reservation reason | Exact bead ID |
| Commit subject | Include the exact bead ID |
| Beads comment | Link relevant commit or thread outcome |

Example:

```text
thread_id="llmux-p3-example-abc"
subject="[llmux-p3-example-abc] Start: preserve request bytes"
reason="llmux-p3-example-abc"
commit="feat(proxy): preserve request bytes (llmux-p3-example-abc)"
```

Keep one thread per bead. Reply in the existing thread instead of creating parallel threads for progress, blockers, review findings, and completion.

### Communication rules

- **Targeted messages:** Send to agents whose work is affected. Do not broadcast routine progress.
- **Start message:** Announce the claim, reserved paths, expected interface impact, and planned verification.
- **Interface change:** Notify adjacent agents before changing a shared type, constructor, package contract, storage schema, or wire behavior.
- **Blocker message:** State the exact blocker, evidence, affected bead, and the event that would unblock work.
- **Acknowledgment:** Require acknowledgment for interface changes, blockers, and decisions, not for routine status.
- **Inbox cadence:** Check at startup, before changing a shared interface, before committing, after pushing, and before choosing the next bead.
- **Prompt response:** Acknowledge messages requiring action before continuing past the next safe boundary.
- **No communication purgatory:** If a ready, non-conflicting bead exists, claim and work rather than waiting for general consensus.

Agent Mail is not a task tracker. Do not create tasks only in Mail. Beads remains the state authority.

## File reservation guard

The Agent Mail pre-commit guard turns reservations into a commit boundary. It refuses a commit that touches a file reserved exclusively by another agent.

Install repository hooks first, then install the Agent Mail guard:

```bash
bin/install-hooks
mcp-agent-mail guard install "$(git rev-parse --show-toplevel)" . --prepush
mcp-agent-mail guard status .
```

Before committing, verify all of the following:

- `AGENT_NAME` matches the identity returned by `macro_start_session`.
- Guard status is active.
- Guard mode is blocking, not warning.
- `AGENT_MAIL_BYPASS` is unset.
- Every staged path is covered by the current bead's reservation.
- No staged path is reserved by another agent.
- The staged diff contains no secret.

Never use `git commit --no-verify`. It bypasses the reservation guard, gitleaks, the authorship guard, and the prose guard.

The guard is a backstop, not permission to ignore conflicts. Reservations must still be taken before the first edit.

If the guard is missing or cannot reach Agent Mail, announce the failure in the active thread and stop before code changes or commits. The single-branch model must not run with only part of its safety system.

## Agent fungibility

Every agent is a generalist. Any agent may implement, test, review, document, diagnose, or repair any ready bead.

Do not assign permanent specialist roles such as backend agent, test agent, reviewer, committer, or coordinator. A temporary activity attached to a bead does not become an identity.

Do not create a boss agent whose memory or availability controls the swarm. Coordination lives in AGENTS.md, Beads, bv output, Agent Mail threads, reservations, tests, and Git.

No subsystem belongs to an agent. File reservations protect active edits, not ownership.

When an agent disappears, replace it. Do not make recovery of its private context a prerequisite. The replacement agent must:

1. Reread this file.
2. Register with Agent Mail.
3. Inspect the stale bead and its thread.
4. Inspect committed code and current diffs.
5. Wait for or confirm expiry of stale reservations.
6. Announce takeover in the same thread.
7. Claim or retain the bead state explicitly.
8. Reserve the required files.
9. Re-establish the test baseline.
10. Continue from durable artifacts.

A lost agent may cost time. It must not cost state or block the graph permanently.

## NTM launch control

NTM manages multiple agent terminals. It is a launch and visibility tool, not a task authority or coordinating intelligence.

Use Beads and bv to determine safe capacity before adding agents. Stagger starts so each new agent observes updated claims and reservations.

At the initial frontier, do not exceed the three beads returned by `br ready --json`, and use fewer agents if edit surfaces overlap.

Different agent programs may be mixed, but they receive the same manual and remain fungible. Model family is not a specialist role.

Useful commands include:

```bash
ntm spawn llmux --cc=1 --cod=1 --agy=1
ntm add llmux --cc=1
ntm status llmux
ntm send llmux --all "Reread AGENTS.md, register with Agent Mail, and choose ready work through bv and br."
ntm palette llmux
```

Do not increase the swarm because agents are idle. Increase it only when the graph exposes more independent ready work.

## Shared working tree rules

Unexpected modifications are normal in a swarm. They usually belong to another active agent.

Never stash, revert, overwrite, reformat, stage, unstage, or delete a change merely because the current agent did not create it.

Do not stop and ask for human guidance about ordinary parallel modifications. Use reservations, Beads, Agent Mail, and the diff to identify the owner and continue on a non-conflicting surface.

If another agent changes a file needed by the active bead:

1. Check its reservation.
2. Read the related Mail thread.
3. Send a targeted coordination message.
4. Agree on an interface boundary or wait for the existing commit.
5. Re-read the file after the other commit lands.
6. Renew the current reservation before editing.

Never assume a file is safe because `git status` was clean a moment ago.

## Code editing discipline

### No script-based code changes

Do not write or run one-off scripts, broad regex replacements, `sed` rewrites, or generated codemods that modify source files.

Edit code deliberately. Review every changed line.

Repository-owned formatters and generators are allowed when the bead requires them. In the shared working tree, scope formatters to files reserved by the current agent.

```bash
gofmt -w path/to/reserved_file.go path/to/reserved_file_test.go
```

Do not run a repository-wide formatting command while another agent may be editing Go files.

### No file proliferation

Do not create variants such as:

- `router_v2.go`
- `router_improved.go`
- `router_new.go`
- `router_fixed.go`
- `router_final.go`

Create a new file only when it represents a real package responsibility, a test file paired with production behavior, an embedded asset required by the bead, or another independently coherent artifact. The zero-code starting point naturally requires new files; the prohibition is against replacement variants and abandoned alternatives.

### Surgical scope

Every changed line must trace to the active bead or a proven defect required to complete it.

Do not refactor adjacent code, rename unrelated identifiers, reformat unreserved files, add speculative extensibility, or implement a downstream bead early.

Remove only imports, variables, functions, or files made obsolete by the current change, and delete a file only with explicit permission.

### No compatibility shims

The project has no released compatibility surface. When a bead changes an internal contract, update the real contract and all current callers directly.

Do not add deprecated wrappers, duplicate APIs, fallback aliases, migration-only abstractions, or parallel implementations unless a bead explicitly requires compatibility.

## Go engineering rules

- **Standard library first:** Use `net/http` for server and upstream calls. Do not introduce a web framework or router dependency.
- **Dependencies:** Add a third-party module only when the active bead requires a capability that the standard library cannot reasonably provide.
- **Dependency edits:** Reserve `go.mod` and `go.sum` before any command that may change them.
- **Formatting:** Run `gofmt` only on reserved Go files.
- **Errors:** Add context while preserving causes with `%w`. Never include credentials, authorization headers, request bodies, or response bodies in errors.
- **Contexts:** Propagate request contexts through upstream calls and storage work. Honor cancellation and deadlines.
- **Bodies:** Close every owned response body. Preserve streaming behavior and do not buffer without a bead-defined bound.
- **Concurrency:** Make ownership explicit. Do not hold locks across network or storage I/O.
- **Interfaces:** Create interfaces only at real injection boundaries or proven package seams.
- **Construction:** Prefer explicit constructor injection. Do not add a dependency injection framework.
- **State:** Avoid mutable package globals. Shared state must have explicit ownership and synchronization.
- **HTTP:** Configure server and client timeouts according to the bead contract. Never rely on unbounded defaults.
- **Logging:** Use structured operational fields. Never log content or credentials.
- **Generated code:** Do not introduce generated code unless the bead identifies the generator, source of truth, and verification command.
- **Build:** Keep `CGO_ENABLED=0 go build ./...` green so the static-binary contract remains true.
- **Linting:** `golangci-lint` in `bin/ci` is the configured aggregate lint gate. Do not add a separate linter without a bead.

Read official current documentation before using a third-party package or an unfamiliar standard-library API. Do not infer a current API from memory when verification is cheap.

## Tests and TDD

Every feature is born with a test. Every bug fix is born with a regression test that fails for the original defect.

All normal tests run with one command and require no manual setup:

```bash
bin/ci
```

### Hermetic by default

Tests use no real credential, public network, upstream account, wall clock dependency, or shared machine state.

The four injection boundaries are:

| Boundary | Test replacement |
|---|---|
| Clock | Named fake clock |
| Permutation source | Named deterministic permutation source |
| Upstream executor | Named fake upstream executor |
| Attempt record writer | Named fake record writer |

Use named fakes with explicit behavior. Do not hide behavior in inline closures or anonymous stubs. A test that requires a real account key is misplaced.

### Required coverage

For every bead, cover the behavior relevant to its contract:

- Happy path
- Empty input
- Boundary values
- Malformed owned routing fields
- Upstream errors
- Cancellation
- Timeout
- Retry boundaries
- Concurrent access where shared state exists
- Streaming interruption where streaming exists
- Credential redaction where errors or logging are touched
- Every applicable proxy invariant

Tests must prove wire preservation where preservation is required. Parsing and reserializing into equivalent JSON is not proof of byte preservation.

### Race detector

`go test -race ./...` is a hard gate from the first package, not a pre-release activity.

Session pinning, per-account rolling windows, attempt recording, retry state, and concurrent stream relay have no complete correctness story without the race detector.

A race failure blocks the commit. Do not classify it as flaky without reproducing the exact invocation environment and identifying a specific cause.

### Test validation

A new regression test must fail when the fix is deliberately removed or broken. A guard that cannot fail is not coverage.

Do not weaken an assertion, add arbitrary waits, skip a test, or serialize concurrent behavior merely to obtain green output.

## Quality gates

Run focused tests while editing. Before every commit, run the complete gate:

```bash
bin/ci
```

`bin/ci` includes or coordinates:

- `gofmt` verification
- `go vet ./...`
- `golangci-lint run ./...`
- `go test ./...`
- `go test -race ./...`
- `CGO_ENABLED=0 go build ./...`
- `govulncheck ./...`
- gitleaks
- Markdown soft-wrap validation
- Public-prose validation

A skipped check is not a pass. Read the closing summary. If a required tool is missing, record the blocker and do not claim the omitted gate succeeded.

The full gate reads the shared working tree. Before running it, announce a short gate window in Agent Mail so other agents can reach a compiling boundary. If another reserved edit causes failure, send the evidence to its holder and wait or coordinate. Do not repair another agent's active files without a reservation handoff.

Every commit on `main` must pass `bin/ci` and be production-ready. There are no intentionally broken commits repaired by a later commit.

## Self-review after every bead

After implementation and before moving to another bead, switch from implementation to adversarial review.

1. Reread the bead and every acceptance criterion.
2. Read the complete diff with fresh eyes.
3. Check correctness, error paths, empty inputs, bounds, cancellation, concurrency, and resource cleanup.
4. Recheck all seven proxy invariants.
5. Search for the same defect pattern in adjacent code.
6. Consider whether the implementation is more complex than the contract requires.
7. Verify every review finding before changing code.
8. Run focused tests.
9. Run `bin/ci`.
10. Repeat the inspection if the first pass found substantive defects.
11. Commit and push before closing the bead.

Do not wait for a prompt to self-review. Self-review is part of implementing the bead.

If repeated self-review continues finding major defects, do not keep patching symptoms. Record the evidence in the bead and thread, then let another fungible agent inspect the approach.

## Cross-agent review

Self-review cannot structurally catch every integration defect. If one agent defines an interface and another calls it incorrectly, the defining agent may never inspect the incorrect caller. Boundary defects require a different agent to trace the combined workflow.

Perform cross-agent review after an integration gate, after adjacent beads land, or when Agent Mail reports an interface change. Do not stop the whole swarm. One available generalist can review while others continue ready work.

A cross-agent review must:

- Read the relevant beads and threads.
- Trace behavior across package and agent boundaries.
- Inspect more than the latest commit when the execution path requires it.
- Check request preservation, routing state, retries, storage, streaming, and shutdown as one flow.
- Reproduce or prove each finding before editing.
- Reserve every file before applying a fix.
- Attach the review to an existing bead or create a focused bug bead.
- Run the affected tests and `bin/ci`.
- Commit and push the fix directly to `main`.

Review code, not agent identity. No agent owns code after it lands.

## Security is continuous

The proxy holds four credentials: one client bearer key and three upstream account keys.

All four credentials:

- Arrive from environment variables.
- Never appear in source, examples, fixtures, snapshots, or committed configuration.
- Never enter the attempt store.
- Never appear in logs.
- Never appear in metrics labels.
- Never appear in error messages.
- Never appear in panic output.
- Never appear in Agent Mail or Beads.
- Never appear in test failure output.

Only `.env.example` with fake values is committed. Never overwrite a real `.env`.

Before every commit:

```bash
git status --short
git diff --cached
```

Inspect every staged file for credentials and private material. The gitleaks hook is the deterministic backstop, not a substitute for inspection.

When touching request parsing, authorization headers, header forwarding, listen addresses, attempt storage, error envelopes, or logs, identify the trust boundary and add the relevant negative test.

`govulncheck` in `bin/ci` is the dependency vulnerability gate. Do not suppress a finding without a bead containing the evidence and decision.

## Best-practices guides

The project vendors no generic engineering guide, and that is a decision rather than an omission. The available Go guide is written for Fiber, Echo, pgx and Ent, which are four of the dependencies the Scope section rules out, so most of it would have to be read as something to ignore. A guide that needs a paragraph of exceptions before it can be applied is not a guide, it is a second scope statement competing with this file.

The binding engineering rules for this project are the Go engineering rules, the proxy invariants, the tests section and the security section above. When a bead needs external guidance, cite the specific upstream document in the bead rather than adding a general reference here.

## Skills

A skill is an operational instruction pack. When a listed skill is available and the work matches it, invoke or load the skill before acting and read its complete `SKILL.md`.

| Skill | Use |
|---|---|
| `agent-swarm-workflow` | Run the shared implementation, communication, review, and completion loop |
| `agent-fungibility` | Preserve generalist roles and replace lost agents from durable state |
| `agent-mail` | Register identities, reserve files, use threads, and operate the reservation guard |
| `beads-workflow` | Create, repair, split, link, or refine beads when execution exposes graph defects |
| `bv` | Select work and inspect graph health through robot-mode commands |
| `dcg` | Understand blocked destructive commands and choose non-destructive behavior |
| `ntm` | Launch, stagger, inspect, and extend the swarm |
| `ubs` | Optional supplementary scan only when installed |

Skills do not override this file, bead acceptance criteria, or project invariants. An optional tool described by a skill does not become a gate merely because the skill exists.

## Git: one shared `main`

All agents work directly on `main` in the same working tree.

Do not create:

- Feature branches
- Agent branches
- Worktrees
- Pull requests
- Temporary integration branches
- Stashes

The single-branch model removes delayed merge reconciliation. It does not remove coordination obligations.

This section overrides the worktree instruction in the stamped `universal-principles` block below: create no worktree, and do every edit in this one shared tree. The block's physical isolation is replaced here by three coupled protections, Agent Mail file reservations, the pre-commit reservation guard, and DCG, and all three assume a single tree; following the block's worktree instead removes an agent's edits from the surface those protections watch, and mixing the two models gives neither the isolation of separate trees nor the coordination the reservation system provides.

### Before editing

1. Confirm the branch is `main`.
2. Run `git fetch origin`.
3. If the working tree and index are clean and local `main` is behind, run `git pull --ff-only`.
4. Never pull, rebase, or switch branches over another agent's uncommitted changes.
5. Claim the bead.
6. Reserve the files.
7. Announce the work.
8. Begin editing.

### During work

- Commit early enough to keep the conflict window small.
- Keep every intermediate commit green and production-ready.
- Do not leave staged changes in the shared index while continuing unrelated work.
- Recheck reservations before editing a file not included in the original lease.
- Notify adjacent agents before changing a shared interface.
- Never modify another agent's reserved file to make a local test pass.
- Treat a changed `HEAD` as normal. Another agent may have committed while the current edit was in progress.

### Shared index safety

The Git index is shared by every agent.

Before staging, inspect:

```bash
git status --short
git diff --cached --name-status
```

If the index contains another agent's staged paths, do not unstage them and do not commit them. Message the owner and wait for that commit boundary.

Stage explicit reserved paths only:

```bash
git add path/to/file.go path/to/file_test.go
```

Never run `git add .`, `git add -A`, or another broad staging command.

After staging, inspect:

```bash
git diff --cached --name-status
git diff --cached
```

The staged diff must contain only the intended bead change, its tests, and directly required documentation.

### Commit messages

Use Conventional Commits and include the bead ID in the subject:

```text
feat(router): preserve request bytes (llmux-p3-example-abc)
fix(store): use local attempt identifiers (llmux-p2-example-def)
test(proxy): cover upstream parameter forwarding (llmux-p5-example-ghi)
```

One logical change per commit. Include multiple bead IDs only when the change genuinely completes an inseparable contract shared by those beads.

Never add:

- `Co-Authored-By:` for an assistant
- `Assisted-by:`
- `Reported-by:` naming an assistant
- `Generated with`
- Tool signatures
- Robot emoji
- Personal names in the subject or body
- Narration about prompts, context, coordination, or review activity

Git author name and email remain the configured human identity. Do not alter authorship metadata.

### Push after every commit

Push immediately after each commit. Unpushed commits are invisible to agents on other machines and are not durable swarm state.

```bash
git push origin main
```

If the push is rejected:

1. Do not force push.
2. Fetch the remote.
3. Inspect `git status`, `git log --oneline --decorate -n 20`, and the remote delta.
4. Coordinate a clean Git boundary through Agent Mail.
5. Wait until the shared working tree and index can safely support integration.
6. Run `git pull --rebase` only from a clean working tree with no other agent's staged or unstaged work.
7. Rerun `bin/ci`.
8. Push again.
9. Continue until the push succeeds.

Never use automatic stashing during pull or rebase.

### Release reservations

Keep reservations until the commit is pushed. After the push succeeds:

1. Close or update the bead.
2. Run `br sync --flush-only`.
3. Send the completion message in the bead thread.
4. Release the file reservations.
5. Re-run triage before selecting more work.

## Landing the plane

A work cycle is not complete until the push succeeds.

Complete these steps in order:

1. Check Agent Mail and acknowledge required messages.
2. Create or update beads for every real follow-up.
3. Finish the active bead or leave its exact state and blocker in the bead and thread.
4. Run focused tests.
5. Run `bin/ci`.
6. Run `git status --short --branch`.
7. Run `git diff`.
8. Run `git diff --cached`.
9. Confirm no secret or unrelated path is staged.
10. Commit the reserved change with its bead ID.
11. Push `main`.
12. Resolve a rejected push without force, stash, reset, checkout, clean, or overwrite.
13. Close completed work and update incomplete work.
14. Run `br sync --flush-only`.
15. Do not stage `.beads`.
16. Send a completion or handoff message in the existing bead thread.
17. Release reservations after the push.
18. Verify the branch is not ahead of `origin/main`.
19. Recheck the inbox.
20. Only then stop or choose another bead.

The working tree may still show changes from other agents. Do not require those changes to disappear. The required Git condition is that the current agent's intended commit is pushed and local `main` is not ahead of the remote.

Never say work is ready for someone else to push. The agent that commits owns the push loop through success.

## Public prose

This repository is public. These rules apply to Markdown, source comments, docstrings, configuration values, commit messages, issue-facing text, release text, and every other published surface.

- **No personal names:** Outside Git author metadata and identity-specific documents, do not publish a person's name.
- **No assistant attribution:** Never credit an assistant, model, agent, bot, or tool as contributor, co-author, reviewer, reporter, or generator.
- **No em dash:** Use a comma, colon, semicolon, or full stop. `bin/ci` enforces this.
- **Soft-wrapped Markdown:** One paragraph is one line. Each list item and table row is one line. `bin/ci` enforces this.
- **Structural emphasis only:** Bold may lead a bullet or label a structural term. Italics may introduce a term. Do not use either for mid-sentence emphasis.
- **Impersonal rationale:** Describe the software problem, consequence, and decision. Do not describe who requested the work.
- **No process narration:** Do not mention prompts, context windows, compaction, coordination events, review activity, or the circumstances under which text was written.
- **No private paths:** Do not publish machine-specific absolute paths.
- **No private artifacts:** Do not publish `local/`, tracker storage, Mail archives, raw planning notes, or private environment details.
- **No audience narration:** A README states what the software does. It does not address an imagined reader or explain who will read it.
- **Low comment density:** Comments explain non-obvious intent and constraints. They do not restate mechanics or narrate implementation.
- **Bead ID exception:** Public documentation and source comments do not carry task IDs. Commit subjects carry the exact active bead ID because it is the required coordination anchor.
- **Repository-present tense:** Documentation describes the software as it exists. Do not narrate a sequence of internal work events.

Fix Markdown wrapping with the repository tool:

```bash
python3 scripts/md-unwrap.py --write .
```

Run `bin/ci` after prose changes.

## Common hurdles

Record a recurring, non-obvious problem here only when it is not already enforced elsewhere. Every row must identify its class.

| hurdle | class | gate |
|---|---|---|
| A gitleaks test using AWS's documentation key (`AKIAIOSFODNN7EXAMPLE`) passes because gitleaks allowlists it. Probing the hook with that value falsely suggests the gate is broken. Use a pattern that is not allowlisted. | prose | none, judgement |

A hurdle promoted to a gate is deleted from this table, not duplicated. The gate becomes the instruction, and a duplicate hurdle would dilute the remaining unguarded knowledge.

Classify a hurdle when adding it:

- `ci`: A named deterministic command can catch it, and it belongs in `bin/ci`. The row remains only until the check is wired.
- `tripwire`: No full check is possible, but a predictable command immediately precedes the mistake. Put the warning at that command.
- `prose`: The issue requires judgment and has no reliable command trigger.

If the exact catching command cannot be named, the hurdle is not `ci`.

---

The block below is project-independent and stamped from a canonical source outside this repository. Do not edit it here.

<!-- BEGIN universal-principles v3 -->
## Working principles

- **The human defines the WHAT; the agent decides the HOW.** Don't wait for line-by-line dictation. Plan first for non-trivial tasks: show the plan + to-do list, wait for approval.
- **Think before coding — don't assume, don't hide confusion.** State assumptions explicitly; if multiple interpretations exist, present them — don't pick silently. If a simpler approach exists, say so and push back. If a task is impossible under the stated constraints, or info is missing, say so — don't guess. (For trivial tasks, use judgment; this is bias, not ritual.)
- **Surgical changes — touch only what you must.** Every changed line traces to the task. Don't "improve" adjacent code, reformat, or refactor what isn't broken; match existing style even if you'd do it differently. Flag unrelated dead code — don't delete it. Remove only the imports / variables / functions your own change orphaned.
- **Chesterton's Fence — find the problem before undoing the decision.** A config, a flag, a workaround that looks arbitrary is a **fence**: someone put it there, probably to fix something that is invisible to you *because the fence is working*. You arrive with no history, so absence of a visible reason is evidence of your ignorance, not of its uselessness. When your fresh measurement contradicts what the human vaguely remembers ("I changed this once, because of some problem"), **your measurement is the suspect first** — it may be measuring the case that *isn't* failing. Go find the original problem, then decide. *(A CIFS share was benchmarked with a big sequential `dd`, looked fast, and the local-disk download dir was "fixed" away — while the actual failure was random writes: par2, unrar, torrent piece-writes. Two wrong commits.)*
- **Goal-driven execution — define the success check, then loop to it.** Turn the task into something verifiable before coding: "add validation" → write tests for invalid inputs, then pass them; "fix the bug" → write a failing repro test, then pass it; "refactor X" → tests green before and after. For multi-step work, state a brief plan with a verify step each.
- **"Flaky" is not a diagnosis — test in the environment the thing actually runs in.** A component that fails *consistently* under automation is being **mis-invoked**, not being unreliable; "it works when I run it by hand" is not evidence that it works. The shell you test in has a TTY, a `$HOME`, an `ssh-agent`, an interactive stdin — the systemd unit, the CI job and the scripted harness have none of those, so a passing manual run can be testing a different program. Reproduce it *there* (start the unit, `env -u SSH_AUTH_SOCK`, `</dev/null`, `--dry-run` to print the real command line) before accepting "unstable" as a cause. **When a fix doesn't change the symptom, stop fixing and go look at what is actually being executed.** *(An interactive-mode flag with no TTY made one harness fail every review panel for weeks, written off as "flaky"; it was the wrong flag.)*
- **KISS — don't solve a problem you don't have yet.** Simplicity isn't "write less code"; it's not building for a need that doesn't exist. Let structure emerge from the code.
- **YAGNI & flat.** No preventive abstractions, no single-use interfaces. Interfaces for real boundaries only. Architecture is *extracted* once a pattern proves itself in real use — never designed up front for a user who doesn't exist yet. Need pulls architecture.
- **Development cost is not your cost — don't let it pick the design.** Choosing between technical options, weight quality, simplicity, robustness and long-term maintainability; don't weight how long the work takes. The estimate comes out in human units — days, weeks — because that is what the training data measured, and the cheaper option then wins on a cost the agent does not pay. This is **not** licence to over-build: KISS and YAGNI decide *whether* a thing is needed, and this decides *how well* it is built once it is. "That would take a week" is not an argument here; "nothing needs this yet" is.
- **Order: make it work → make it right → make it fast** (Kent Beck), in that order. Most over-engineering is doing "right"/"fast" before a working thing exists to justify it.
- **Flag scope creep — a standing duty, not a suggestion.** When a solo tool starts being framed as a public / multi-user / multi-tenant / plugin-system / configurable-N-backends platform before a real, present need exists, STOP and ask: "Is this needed now?" Justify future-proofing against a need that exists *today*.
- **No silent decisions (comprehension debt).** Never make a silent architectural or design call — state it and record the rationale, so the reasoning is recoverable later.
- **Real decisions are presented in the chat, in isolation — never via popup.** When a design/architecture/scope/trade-off decision arises, surface it on its own: the options, what each means, pros/cons/trade-offs, and a recommendation — then decide together. Don't bury it mid-text or bundle it with other topics, and don't compress it into a quick-pick widget (e.g. AskUserQuestion) — the widget skips the reasoning and overlays the explanation. Widgets are for trivial short-answer picks only.
- **Long answers are written to be scanned, not read twice.** For recaps, status reports, batch reviews, plans, and any comparison of options: lead with the outcome in one line, then break the body into bullets and **bold** the load-bearing terms. Options are always a list — one bullet per option, the recommended one marked — never a paragraph the reader has to parse to find the choices. Reserve unbroken prose for short arguments; a wall of paragraphs costs more in re-reading than the structure would have cost in words.

## Git: branches, commits, PRs, comments

- **Ask the repo for its default branch; never assume one.** Repos differ — `master` and `main` are both common, often in the same person's account — and a wrong guess sends a PR to a branch that does not exist, or, worse, has you "fixing" a URL that was right all along. `git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`, or `gh repo view --json defaultBranchRef -q .defaultBranchRef.name`. Never commit directly to it: branch, then PR.
- **A new repo starts on `main`.** That is the preferred name, and `init.defaultBranch` is set to it, so `git init` produces it without anyone choosing. It settles new repos only: an existing one keeps the branch it has, because renaming breaks open PRs, CI filters, deploy hooks and every permalink into the tree, and buys nothing. The rule above still governs everything already in existence — ask, never assume.
- **Branches** — Conventional Branch (conventionalbranch.org): `<type>/<kebab-description>`, types `feature/`, `bugfix/`, `hotfix/`, `chore/`, `release/`, `docs/`.
- **Commits** — Conventional Commits (conventionalcommits.org): `<type>(scope): <description>`, types `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `build`, `perf`, `style`. Breaking change → `!` after the type or a `BREAKING CHANGE:` footer.
- **Atomic commits** — one logical change per commit, each independently green and revertible. Never `git add .` blind; split unrelated changes.
- **Always work in your own worktree — mandatory, not conditional.** Parallel sessions are opened freely and nothing signals their existence to you, so a "check whether another session is here first" step can never be reliable — the honest answer is always "maybe". The only collision-proof arrangement is structural: keep the main working tree on the default branch as a clean reference and **never work in it** — before your first write (commit, branch, rebase, stash; read-only exploration is exempt), create your own worktree and do everything there: `git worktree add ../<repo>-<task> -b <your-branch> <origin>/<default-branch>`. Do this **whether or not** you believe another agent is running — that belief is exactly what you cannot verify. Report which worktree/branch you used; remove it once merged. Only the human can see all the open sessions.
- **Pull requests** — describe **what + why**. *What*: a 1–3 line summary. *Why* (the bulk): decisions, trade-offs, rejected alternatives. The diff shows the what; the PR explains why.
- **Comments** — always **WHY, not WHAT**: explain intent, never restate the obvious mechanics. Keep existing comments; they carry intent.

## Code style (baseline)

- Functions: 4–40 lines, one thing each (SRP). Files: under ~500 lines, split by responsibility.
- Names specific and unique — avoid `data`, `handler`, `Manager`, `util`.
- Explicit types. Early returns over nested ifs; max ~2 levels of indentation.
- Inject dependencies; wrap third-party libs behind a thin interface this project owns.
- No duplication — but don't extract *too early*. Tolerate duplication while the pattern is still forming; extract the abstraction *from* proven, repeated code, never ahead of it.
- **Refactoring is not automatic.** After a large feature, list refactoring candidates (files > ~500 lines, duplicated logic, long functions, hardcoded config) and ask before pruning — the human decides, the tests are the safety net. Consolidate when the thing works and the seams are obvious, not before.
<!-- END universal-principles v3 -->
