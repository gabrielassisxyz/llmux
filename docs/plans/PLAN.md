# llmux implementation plan

## 0. How to read this document

This section states the conventions the rest of the document is written in. None of them is a requirement on the software. They are what a reader needs in order to cite a rule, to change one, or to tell a definition from a restatement, and each is numbered so that it can be cited as §0 rule 4.

1. Two numbering spaces exist for the word invariant. §4 numbers the non-negotiable invariants 1 to 24, and §25 numbers the concurrency invariants 1 to 33. The same property can appear in both at different numbers, so every reference names its space: §4 invariant 18 is the one about injected usage fields, and §25 invariant 31 is the one about the rolling window. A bare number resolves in two places and therefore in neither.
2. §23, §24, §29 and §32 restate requirements defined elsewhere and are never the source of one, which §1 states as a rule of this document. Read a statement in one of them as a pointer to the section that defines what it describes, and when the two disagree, the restatement is the defect. §24.6 is the single exception and it runs the other way, defining the clock rules and the future-timestamp clamp that §10.3, §15.3, §16.3 and §28 cite as their source. §24.5 in particular defines nothing: the rule that a dispatch which cannot be recorded is refused rather than sent lives in §15.11 and §12 step 18, and a citation of that rule belongs there.
3. §32 reads like acceptance criteria and is restatement. The acceptance obligation for a requirement is its §28.x test bullet read together with the section that defines the requirement, and §32 is a checklist over those pairs that introduces none of its own. A line of §32 that no §28 bullet covers is a hole in §28 rather than a requirement §32 owns.
4. Five distinct time anchors exist and are trivially conflated, so anything referring to a timestamp names the boundary it means: reservation (`reserved_at_us`), the `http.Client.Do` invocation (`event_at_us` on a dispatch row, `attempt_duration_us`, `time_to_first_event_us`, and the rolling window), handler start (`logical_elapsed_us`, and `started_at_us` on an unrouted row), phase start (`selection_wait_us`), and full completion (`finished_at_us`, and the affinity hour of §16.2). The first two are one store commit apart, and §14.1, §15.3, §15.5 and §17.1 each spend a paragraph keeping that displacement out of a quantity named for upstream behavior.
5. There are two clocks. Durations are monotonic and persisted instants are wall, by §24.6, with one documented exception in §16.3. A test that advances both together proves nothing about which one the implementation read, which is why the §28 preamble requires the assertion carrying a clock rule to advance one clock while holding the other.
6. The three conformance matrices are single tables by requirement of §1 and are not split: retry classification from §21.2, response commitment from §13.6, and account-health transitions from §20. Each is the one table that the unit tests and the full-handler tests both read. A second copy made for one test level, one caller or one phase is what lets a row change in one place and not the other with nothing failing.
7. A vocabulary is never separated from its constraints. Each of §15.6, §15.7, §15.7.1 and the `usage_observation` values declared in §15.5 pairs with a line in §15.8 that fixes the enum and an assertion in §28.13 that exercises it. A value added to a list without the matching constraint and assertion is a free-text column, which §15.7.1 states as the reason the two retry vocabularies were written down at all.
8. A column change is a three-place change: the declaration in §15.5, the constraint in §15.8, and the named query recipe in §30.3 that reads it and that §28.13 executes against a seeded store in CI. That third place is also the test §15.5 applies before accepting a column at all, which is why the columns it declines are declined for having no reader rather than for costing storage.
9. The word local before a status code means the proxy produced that response itself: it carries §22’s error envelope and one of §22’s codes, and it is never an upstream status relayed downstream. A status written without the word is upstream’s, relayed unchanged. The distinction is load-bearing wherever both can occur, since a local 429 says this proxy declined to keep queueing while a relayed 429 says the account was refused, and §30.7 is a precondition on callers precisely because the first of those is new.
10. Four units of work nest, and a sentence means different things depending on which one it names. A logical request is one client call and holds one `logical_request_id`. An account-selection phase is one attempt to acquire a lease, is numbered by `selection_no`, and is bounded at 60 seconds. A dispatch, also called an attempt, is one `http.Client.Do` with one `attempt_id`, and it is what the per-account ceilings and the four-dispatch budget count. A selection skip is a candidate rejected locally: it is a durable row, it is none of the three above, and it consumes no capacity.
11. A rejected alternative is a closed decision recorded where the decision was made, not an open question. A passage that names an alternative and states that it is not taken says what was refused and why, and it belongs to the section that owns the rule the alternative would have changed. Reopening one is an edit to that section, and it has to answer the argument recorded there.
12. §9.2 defines the policy constants it lists, and every other appearance of one of those numbers is a restatement of a row of that table, so changing a constant is an edit to §9.2 and to every section that quotes it. Constants that table does not carry are defined where they are stated: the backoff ladder in §21.3, the per-class retry budgets in §21.2, the cooldown threshold and the ten-minute `Retry-After` clamp in §20.2, the 32-byte key floor in §6.3 and §9.1, and the 256-byte session-header cap in §6.6. Two different rows of that table are both five seconds, the saturated-pin grace and the minimum deadline runway before a retry dispatch, and the prose around each spells it as five seconds.
13. The three consumers are named without being introduced, so what each name stands for is collected here: `pi` is the primary streaming caller and the one that runs multi-turn tool loops, `kernl` makes non-streaming calls, and `eod` is the once-a-day summariser that runs with no session file and is therefore the only sessionless caller. The names first appear in §15.3 and §23.15 and are described only in §30.3, §30.6 and §30.7.

## 1. Purpose and implementation contract

This document is the implementation contract for `llmux`. Implementation work should follow it without reopening the closed decisions in [`IDEA.md`](../../local/plans/IDEA.md).

`llmux` is a single-user, single-machine OpenAI-compatible routing proxy. It exposes two HTTP resources, routes seven logical model aliases across three Ollama Cloud accounts, preserves conversation-to-account affinity, enforces account-wide ceilings, relays streaming and non-streaming responses, and writes a durable dispatch-admission ledger plus append-only attempt records to one embedded SQLite store.

The exact upstream model strings and the route-owned presets are a prerequisite of implementation, not a placeholder to be resolved while coding. Phase 0 transcribes them from the current deployment, verifies them against the real upstream, and replaces every provisional value in this document before production code is accepted. This is deployment inventory, not runtime backend configurability.

When two sections appear to pull in different directions, the non-negotiable invariants take precedence, followed by the external HTTP contract, the routing and retry state machines, and finally implementation convenience. Tests must encode that same precedence.

Sections 23, 29, and 32, and all of section 24 except §24.6, restate behavior that is defined elsewhere and introduce none of their own. When one of them disagrees with the section that defines what it describes, the restatement is the defect and the fix belongs there, not in the rule. §24.6 is the carve-out and it runs the other way: it restates which clock each quantity already follows, but the general clock rules and the future-timestamp clamp are stated there and nowhere else, and §10.3, §15.3, §16.3, and §28 cite it as their source, so a section that disagrees with §24.6 about one of those is itself the defect. Duplication is how a contradiction gets in here: a restatement and its rule drift apart, nothing fails when they do, and whoever finds them next has to work out which one was meant. The three state machines this document restates most therefore get one table-driven conformance matrix each: retry classification from §21.2, response commitment from §13.6, and account-health transitions from §20. Each matrix is the single source the unit tests and the full-handler tests both read, because two levels of test reading one table is what stops a restatement and its rule from drifting apart without anything failing.

## 2. Goals

- Serve the three known local consumers at `http://localhost:4000`.
- Authenticate callers using one shared bearer key.
- Support the seven base aliases and their `-k1`, `-k2`, and `-k3` account-pinned variants.
- Preserve the complete raw top-level `messages` value without changing any field inside it.
- Keep a conversation on one account for a sliding hour whenever possible.
- Enforce exactly:
  - 60 dispatched upstream attempts in any rolling 60-second window per account.
  - 12 live upstream attempts per account.
- Apply those ceilings across every alias, model, session, and retry that reaches the same account.
- Retry only failures for which retry can plausibly change the result.
- Disable revoked/lapsed credentials immediately and cool repeatedly rate-limited accounts.
- Relay final upstream responses without transforming JSON or SSE content.
- Record enough attempt metadata to reconstruct account use, spills, retries, local skips, latency, and upstream-reported token counts.
- Durably record every dispatch admission before the network call it authorizes, so every dispatch that left this process has evidence that it started, whatever the process learned about it afterwards.
- Refuse dispatch for one full rolling window after every process start, measured on the monotonic clock, so a restarted process cannot exceed the rolling ceiling it claims to enforce and no wall-clock adjustment can weaken that.
- Bound request, replay, and precommit memory in aggregate, not only one request at a time.
- Return a proxy-owned request ID that correlates a client-visible result with its durable rows.
- Ship as one cgo-free static Go binary with no runtime, framework, container, database server, or ORM.

## 3. Non-goals

- Tenancy, user management, roles, per-consumer policy, or multiple proxy credentials.
- A provider abstraction, backend registry, plugin system, or runtime-configurable account/model set.
- An admin API, dashboard, metrics endpoint, web interface, configuration UI, or log-query API.
- OpenAI endpoints other than chat completions and model listing.
- Support for responses, embeddings, images, audio, assistants, files, or batches.
- Request parameter validation or capability negotiation.
- Prompt inspection, tool/message normalization, moderation, redaction, or transformation.
- Proxy-side prompt caching.
- Monetary cost, price tables, budgets, or billing.
- Active health checking, scheduled probes, model discovery, or warm-up traffic.
- Horizontal scaling or coordination among multiple proxy processes.
- Automatic account-key reload or runtime route changes.
- Automatic retention, rotation, compaction, or deletion of durable records. The cold archive procedure of §30.4 exists and is operator-invoked, offline, and deletes nothing.

## 4. Non-negotiable invariants

1. The raw JSON value of top-level `messages` is copied byte-for-byte. No field inside that array is decoded and re-encoded, renamed, reordered, added, removed, or translated.
2. Apart from locating the routing alias and applying fixed route-owned top-level fields, request parameters remain opaque.
3. Every primary key and correlation key owned by the proxy is generated by the proxy.
4. The process makes no upstream request except while serving `POST /v1/chat/completions`.
5. Prompt and completion text never enter SQLite, structured logs, temporary files, diagnostics, or local error messages.
6. Rate state has exactly three keys: `k1`, `k2`, and `k3`.
7. No alias, deployment, or model creates its own rate bucket.
8. There is no currency type, cost column, pricing configuration, or cost calculation.
9. Durable records live in one embedded SQLite store accessed through handwritten SQL.
10. Each actual upstream dispatch has a committed `dispatch_admission` row written before `http.Client.Do`.
11. Each actual upstream dispatch consumes one RPM slot even if it fails to dial, returns an error, is canceled, or is a retry.
12. Each dispatched attempt produces at most one terminal append-only attempt row in addition to its admission row.
13. Local selection skips produce separate append-only rows and do not count as upstream attempts.
14. No retry occurs after the downstream response is committed.
15. Coordinator state is never locked while performing network, database, logging, or downstream I/O.
16. All request-owned resources are bounded by a deadline, size limit, permit, or explicit lifecycle, and their sum across concurrent requests is bounded too.
17. Every untouched top-level JSON member retains its original raw value bytes and relative order, including unknown members and duplicate unknown members. Only the routing-owned top-level values may differ.
18. The proxy never injects `stream_options.include_usage`, never alters `stream`, and never changes the response stream to improve observability. Missing usage remains missing.
19. Every account-acquisition phase ends in one atomically reserved and durably admitted dispatch, one explicit terminal selection-failure record, or one admission-store failure. It cannot wait indefinitely.
20. A committed response that later breaks is aborted as a transport failure; the proxy must not make a truncated upstream body look like a cleanly completed HTTP response.
21. An account disabled by an authentication failure stays disabled for the process lifetime. No request, alias or code path re-enables it.
22. No client-supplied query string is ever forwarded upstream.
23. Raw session identifiers are never persisted or logged. Only keyed digests are stored.
24. The rolling rate window is measured over `http.Client.Do` invocation instants. A reservation occupies a slot from the moment it is granted, but the timestamp the window is defined against is installed at the dispatch boundary.

## 5. Fixed design decisions

| Area | Decision |
| --- | --- |
| Language | Go 1.26 |
| Release toolchain | An exact Go 1.26 patch release, pinned in the module and in CI |
| HTTP server | `net/http.Server` and `http.ServeMux` |
| Upstream client | `net/http.Client` with a shared configured transport |
| Persistence | SQLite through `modernc.org/sqlite` |
| Database access | `database/sql` and handwritten parameterized SQL |
| Dependency injection | Manual constructor injection |
| Internal structure | Small cohesive internal packages; no clean-architecture or DDD ceremony |
| Request replay | Bounded in-memory buffering |
| Maximum request body | 64 MiB |
| Non-streaming response precommit buffer | 8 MiB, followed by unchanged progressive relay if exceeded |
| Rate algorithm | Exact rolling timestamp window and in-flight counter |
| Scheduler synchronization | One coordinator mutex covering all account and session state |
| Account-acquisition ceiling | 60 seconds or the remaining logical deadline, whichever is shorter |
| Pinned saturation | Reopen-aware wait of at most five seconds, then spill |
| Session TTL | Sliding one hour, refreshed on successful completion |
| Retry limit | At most four dispatched attempts per logical request |
| Retry placement | 429 and upstream 504 prefer another account; an initial 5xx/408/transient-network retry prefers the same account to retain cache |
| Request headers | Fixed end-to-end allowlist; all proxy-internal and hop-by-hop headers are removed |
| Dispatch evidence | One synchronous append-only admission row committed before every `http.Client.Do` |
| Terminal logging | One synchronous SQLite transaction per terminal routing phase; pending skips and its dispatch/failure are committed together |
| Disabled-account recovery | Restart only |
| Process model | One proxy process |
| Backend definition | Fixed source catalog |
| Upstream base | `https://ollama.com/v1` |
| Static build | `CGO_ENABLED=0` release build |

The toolchain row names a patch release and not only a language version, because a language version in a module is a floor that the toolchain in front of it may sit above or below, and the patch releases are where the standard library’s own security fixes land. This document deliberately does not carry the number. The exact patch is chosen at Phase 1 from the then-current 1.26 release and advanced by reviewed updates afterwards, for the same reason §8.4 refuses to carry provisional model strings: a version written into prose rots between the writing and the building, and the one copy that is checkable is the module file the build actually reads. Phase 1 owns that choice and §31 gates it there, so the pin exists before any code is written against it rather than being noticed at the first release, and §28.17 confirms on every build afterwards that the pinned toolchain is the one that ran.

The fixed Ollama Cloud OpenAI-compatible base is documented by [Ollama’s official integration documentation](https://docs.ollama.com/integrations/droid). Which request fields and reasoning values that base accepts is documented separately, by [Ollama’s OpenAI compatibility documentation](https://docs.ollama.com/api/openai-compatibility).

## 6. External HTTP contract

### 6.1 Listen address

- Default: `127.0.0.1:4000`.
- `LLMUX_LISTEN_ADDR` may change the literal loopback address or the port.
- Only an IPv4 loopback literal or `::1` is accepted. A hostname, a wildcard, the unspecified address, and any non-loopback IP are fatal configuration errors. Remote exposure belongs behind a separately configured reverse proxy that terminates TLS and authenticates, not behind a flag on this process.
- Accepting a hostname would turn the loopback rule into a question about name resolution. What a name resolves to at startup is not what it resolves to later, and a name with several records is a choice of address the operator never made. A literal is checkable without asking anything, which is what lets §26.4 claim the binding is loopback rather than that it was loopback at the moment somebody looked.
- TLS is not added because all specified consumers are local, which is the same decision as the rule above rather than a second one: a shared bearer key in cleartext on a wildcard bind is the failure that rule prevents.
- The local server speaks HTTP/1.x only, configured explicitly through `http.Server.Protocols` rather than left to whatever a given Go release defaults to. Cleartext HTTP/2 requires prior knowledge that none of the three consumers use, and restricting it makes the committed-response abort semantics one path instead of two, the second of which could never be exercised here. The upstream transport already names its protocols explicitly, and this is the same rule applied to the side that faces the consumers.
- No CORS behavior is added.

### 6.2 Routes

Exactly these resources exist:

- `POST /v1/chat/completions`
- `GET /v1/models`

Behavior for everything else:

- Unsupported method on a known path: 405 with `Allow`.
- Unknown path: 404.
- Trailing-slash forms are not aliases for the canonical paths.
- No request to an unknown path reaches upstream.

### 6.3 Authentication

Both endpoints require:

`Authorization: Bearer <LLMUX_PROXY_KEY>`

Rules:

- The bearer scheme is case-insensitive.
- Exactly one `Authorization` header field, carrying exactly one non-empty bearer credential, is accepted. Several fields is a rejection, not a search for one that matches.
- The configured proxy key must be at least 32 bytes, checked at startup.
- The proxy key must differ from every upstream account key, so that a copy-paste cannot hand a local client a credential that upstream also honours.
- Startup keeps a SHA-256 digest of the proxy key. Authentication hashes the presented value and compares two fixed-size digests in constant time. Comparing the raw values, even in constant time, still branches on length and so leaks the key length to anyone who can time it; hashing first removes that channel and makes the comparison genuinely uniform.
- Missing or invalid credentials return 401 and `WWW-Authenticate: Bearer`.
- Authentication is performed before reading a potentially large request body.
- Bad authentication does not create an attempt row because no account was considered.
- Authentication failures may produce a sanitized structured warning.
- No credential value or credential fingerprint is logged, persisted, or echoed.

### 6.4 `GET /v1/models`

This endpoint is entirely local:

- It performs no upstream request.
- It performs no database query.
- It performs no account-health evaluation.
- It returns all 28 client-visible aliases.
- It remains stable when accounts are saturated, cooling, or disabled.
- It is both the picker catalog and cheap process liveness response.

The response is `{"object":"list","data":[...]}` with `Content-Type: application/json`. The envelope is stated because a client picker that reads `data` finds nothing if the array is returned bare, and that is a failure with no error attached to it.

Ordering is deterministic. For each base alias, return:

1. Base alias.
2. `-k1`.
3. `-k2`.
4. `-k3`.

Each model object contains:

| Field | Value |
| --- | --- |
| `id` | Client-visible alias |
| `object` | `model` |
| `created` | `0` |
| `owned_by` | `llmux` |

`created` is zero because the proxy has no meaningful creation timestamp and must not fabricate one.

### 6.5 `POST /v1/chat/completions`

The proxy accepts streaming and non-streaming OpenAI-shaped bodies.

It locally interprets only the routing envelope:

- The body must be syntactically valid JSON.
- The top-level value must be an object.
- Exactly one top-level `model` member must exist.
- `model` must be a string.
- It must name a fixed catalog alias.
- At most one top-level `stream` member may exist, because the proxy reads it to select relay behavior.
- Nesting depth must not exceed 256.

Everything else is opaque, including:

- `messages`
- `tools`
- `tool_choice`
- Tool-call IDs and arguments
- Sampling parameters
- Response formats
- Reasoning fields not owned by the route
- Unknown extensions
- Unsupported upstream values

The proxy does not reject a parameter merely because it is unknown or unsupported.

Routing-envelope failures:

| Condition | Result |
| --- | --- |
| Invalid JSON | Local 400 |
| Non-object top level | Local 400 |
| Missing/non-string `model` | Local 400 |
| Duplicate top-level `model` | Local 400 |
| Duplicate top-level `stream` | Local 400 |
| Nesting depth over 256 | Local 400 |
| Unknown model alias | Local 404 |
| Body over 64 MiB | Local 413 |
| Compressed request body | Local 415 |

Duplicate `model` and `stream` fields are rejected because routing and relay selection would otherwise depend on which duplicate a parser chooses. This is an envelope ambiguity, not model-parameter validation: duplicate members the proxy does not read remain untouched and cross to upstream in their original order.

The depth limit is a parser resource bound of the same kind as the 64 MiB body limit, not an opinion about the request. Nothing a real client sends approaches it, and it is what lets the scanner promise it cannot be driven into unbounded recursion by input.

### 6.6 Session header

The fixed session header is `X-Session-ID`.

- Missing or empty means no affinity.
- A non-empty value is an opaque, case-sensitive session identifier of at most 256 bytes.
- Exactly one `X-Session-ID` header field is accepted. Several fields is a local 400, which is the answer §6.3 already gives to several `Authorization` fields and for the same reason: taking the first would make a conversation’s affinity depend on an implementation’s choice among values the client sent.
- It is not interpreted or rewritten.
- It is not forwarded upstream.
- It is reduced to `HMAC-SHA-256(LLMUX_AFFINITY_HMAC_KEY, sessionID)`, and only that versioned digest, named `session_key`, exists in memory or in SQLite.
- Affinity is keyed only by `session_key`, not by alias or model.
- A value over 256 bytes returns local 400. That rejection and the several-fields rejection above both carry the `invalid_session_header` code of §22, and both are members of `unrouted_request` by §15.3, so the row §15.8 constrains to §22's vocabulary has a value to hold. Neither is an invalid routing envelope: that code names the routing fields of the body, and these two are header rejections that happen before a body is read.

Affinity needs stable equality, not the caller’s string. The header is client-chosen text of arbitrary content and length, and storing it verbatim puts unbounded unreviewed input into a durable log that outlives every conversation in it. A keyed digest keeps every routing decision identical, bounds the column, and leaves nothing in the store to read or to guess. The claim is about what the proxy retains, not about what the process can touch: the inbound `http.Request` necessarily holds the raw header until the handler returns, and what the rule forbids is making a second copy of it, carrying it past validation, or writing it anywhere at all.

### 6.7 Request content encoding and content type

- Request `Content-Encoding` must be absent or `identity`.
- Compressed requests cannot be patched and retried while preserving the raw body contract, so they return 415.
- `Content-Type` is not used for capability validation.
- The endpoint nevertheless parses the body as JSON because its wire contract is JSON.

### 6.8 Response correlation

- Every authenticated chat request is assigned its proxy logical request ID before any body processing.
- Every authenticated chat response carries it as `X-LLMux-Request-ID`, whether the response came from upstream or was generated locally.
- An upstream header of that name is removed before the proxy-owned value is set, so the value a client reads is always the proxy’s.
- A request that ends with a local response before any account-selection phase begins is the one case that produces no attempt row, so it appends one `unrouted_request` row instead, and the identifier resolves in exactly one of the two tables.

Without it, a consumer that saw a failure has a timestamp and nothing else to find the row with, and the one identifier both sides already share is the upstream response ID, which §4 invariant 3 forbids using as a key and §13.3 forbids storing. A proxy-generated random ID gives the operator a direct key into SQLite while exposing nothing about upstream.

## 7. Request transformation contract

### 7.1 Body scanner

A dedicated lexical top-level JSON scanner must:

1. Find the boundaries of the top-level object.
2. Read top-level member names.
3. Locate each member’s raw value span.
4. Correctly skip nested objects, arrays, strings, escapes, numbers, booleans, and nulls.
5. Avoid decoding nested message content.
6. Return routing metadata and a rewrite plan.
7. Reject malformed syntax and ambiguous route-owned duplicates without panicking.
8. Track depth with an iterative state machine or an explicitly bounded stack, so input nesting cannot consume the Go call stack.
9. Retain spans only for the members the proxy reads or replaces. A body with millions of unrelated top-level members must not produce a second body-sized metadata structure.
10. Compare top-level member names after decoding JSON string escapes, so that `"model"` and `"\u006dodel"` are one member and therefore a duplicate. The rule covers `stream` and the active route-owned injection key equally.
11. Decode only those short routing names. A decoded copy of `messages`, or of any other opaque value, is forbidden: it would be the second body-sized allocation §7.2 exists to avoid.

The implementation must not unmarshal the request into a map and marshal it again.

Comparing member names as raw bytes is the tempting shortcut, and it reopens the ambiguity §6.5 closes. A body carrying `"model"` and its escaped spelling presents the scanner with two different names, so one is rewritten and the other crosses untouched as an unknown member, while upstream’s own parser decodes both, sees a duplicate, and keeps whichever its implementation prefers. That is routing decided by somebody else’s parser, which is precisely what the duplicate rules exist to prevent.

### 7.2 Rewrite operation

The rewrite plan describes the upstream body as an ordered list of immutable byte segments rather than as a second copy of the document:

- Untouched regions are spans into the original body buffer, referenced and never copied.
- Only a replaced value or an appended route-owned member allocates a new segment, and those are small.
- The raw top-level `model` value is replaced.
- The value of a route-owned top-level injection is replaced if present.
- A missing route-owned injection is appended immediately before the closing top-level brace.
- `Content-Length` is the checked sum of the segment lengths.
- Every attempt builds a fresh reader over the same immutable segments, so a retry replays the identical bytes without a new allocation.

The original body buffer is therefore the only body-sized allocation, and it lives until no further replay is possible. There is no second rewritten body.

In particular:

- The complete raw `messages` value is byte-identical.
- Top-level unknown fields retain their original order and raw values, including duplicate unknown keys.
- Whitespace inside untouched values is preserved.
- Duplicate route-owned keys are rejected rather than partially rewritten.
- `messages` can never be a configured injected key.
- Untouched numbers never transit through `float64`; forms such as large integers, exponent notation, and negative zero retain their exact spelling.
- Untouched strings retain their exact escape spelling rather than merely decoding to an equivalent Unicode value.

### 7.3 Fixed route-owned injections

| Client alias | Upstream model | Injection |
| --- | --- | --- |
| `deepseek-v4-pro-max` | `deepseek-v4-pro` | `reasoning_effort = "max"` |
| `deepseek-v4-pro-high` | `deepseek-v4-pro` | `reasoning_effort = "high"` |

Other aliases do not inject parameters.

All account-pinned variants inherit their base alias’s model and injection.

If the client supplied the route-owned field, the preset overrides it because the alias itself names that preset.

This override is routing resolution, not general parameter validation. The proxy neither checks whether the selected upstream supports the stamped value nor rejects any unrelated reasoning field.

The injection set is closed:

- No alias injects `messages`, `stream`, `stream_options`, `tools`, or any response-shaping field.
- The proxy does not add `stream_options.include_usage`; clients that want streaming usage must request it themselves.
- A missing usage object therefore produces nullable token fields rather than a mutated request or response.

One of the two presets rests on something this document cannot check. What Ollama’s public OpenAI compatibility page lists for `reasoning_effort` has changed, and it would not settle the question even if it were stable, because a documented value establishes that the API shape accepts it and not that a deployed model treats it differently from its neighbour. The Phase 0 gate must establish against the real endpoint that `reasoning_effort="max"` is accepted and behaves differently from `high`. If it is rejected, the alias is a request that always fails; if it is silently coerced, the alias is a lie told to the client picker, which is worse because nothing reports it. Either way the entry needs a documented alternative mapping or removal before Phase 1, and it must not quietly become a second name for `high`.

## 8. Fixed route catalog

### 8.1 Base routes

| Client base alias | Upstream model | Eligible accounts |
| --- | --- | --- |
| `kimi-k2.7` | `kimi-k2.7-code:cloud` | `k1`, `k2`, `k3` |
| `kimi-k2.6` | `kimi-k2.6:cloud` | `k1`, `k2`, `k3` |
| `glm-5.2` | `glm-5.2:cloud` | `k1`, `k2`, `k3` |
| `glm-5.1` | `glm-5.1:cloud` | `k1`, `k2`, `k3` |
| `deepseek-v4-pro-max` | `deepseek-v4-pro:cloud` | `k1`, `k2`, `k3` |
| `deepseek-v4-pro-high` | `deepseek-v4-pro:cloud` | `k1`, `k2`, `k3` |
| `deepseek-v4-flash-max` | `deepseek-v4-flash:cloud` | `k1`, `k2`, `k3` |

These upstream strings are transcribed from the deployed route catalog. Do not infer differences from alias spelling and do not make them environment-configurable.

### 8.2 Account-pinned variants

For every base alias `a`, generate exact catalog entries:

- `a-k1`
- `a-k2`
- `a-k3`

Each variant:

- Inherits the upstream model.
- Inherits route-owned injections.
- Has exactly one eligible account.
- Is returned by `/v1/models`.
- Uses the same real account limiter as every other route to that account.
- Never spills to a different account.
- Overrides a conflicting session pin for that request.
- Updates a supplied session pin only after a completely successful response, preserving future cache locality on the explicitly selected account.

Arbitrary `-kN` suffix parsing is not allowed. Only exact generated catalog entries resolve.

### 8.3 Catalog startup validation

Startup must assert:

- Exactly seven base entries.
- Exactly 28 public aliases.
- Unique public aliases.
- Non-empty upstream model values.
- Only `k1`, `k2`, and `k3` account references.
- Each base route references all three accounts exactly once.
- Each pinned route references one account.
- Only the two declared pro routes inject `reasoning_effort`.
- No injection targets `messages`.
- Generated pinned routes match their base route’s model and injection.

A catalog validation failure is fatal.

### 8.4 Deployment-inventory gate

A document that calls itself an implementation contract cannot carry provisional values into the code it specifies. Before implementation:

- Replace every provisional upstream model string with the exact value the current deployment sends.
- Send one small bounded request per distinct upstream model and route-owned preset, per account, directly against the upstream base.
- Record only pass/fail, model ID, preset, account label, timestamp, and status code. Never record request or response content.
- Treat an unresolved mapping, an unsupported preset, or a model that one account cannot reach as a blocking specification defect rather than as something the implementation discovers later.

These requests are made by the operator against the upstream, not by the proxy. Nothing here contradicts the invariant that the process makes no upstream request except while serving a chat completion, because at this point the process does not exist.

The alternative is not free: a provisional string that survives into the catalog fails at the first real request, and a preset that upstream ignores fails at no point at all, which is the case this gate exists for.

#### Gate outcome

The gate is closed. Phase 1 builds on the findings below rather than on the paragraphs above, and the recorded evidence sits in the operator's inventory records alongside the bead that produced each line.

- **Upstream model strings.** Transcribed into §8.1 from the deployed route catalog. The provider prefix carried there is internal routing and does not cross to upstream, so it is stripped.
- **Reachability.** Every one of the seven route entries answers on all three accounts, 21 of 21. The eligible-account set stands as written and needed no correction.
- **`reasoning_effort="max"`.** Supported and distinct, so both pro aliases stay as written. It is not a second name for `high`: the request carries a fixed extra preamble, and on a prompt that requires multi-step work `high` finishes unaided while `max` runs past a budget four times the size of the answers `high` returns.
- **Revoked credential.** Upstream answers 401, with no `WWW-Authenticate` and no stable machine-readable code. The account-disablement rule stands as written and the conditional 403 code projection is not built.
- **Response `Content-Encoding`.** Identity throughout, for every advertised `Accept-Encoding`, streaming and non-streaming. The bounded observation decoder of Phase 6 is not built.
- **Rate-limit headers.** No numeric family, on any account. The two nullable columns and their migration are not built, and the weekly ceiling re-derivation stays one-sided, bounded by sustained 429s under the local guard and by nothing from above.
- **`Retry-After`.** Not observed: no 429 and no 5xx occurred across the inventory, so its wire form on this provider is unrecorded and both forms ship as planned. `Date` is present on every response, in HTTP-date form, so the absolute form is derivable against upstream's own clock and the no-usable-`Date` path is the rare one rather than an assumed one.

## 9. Configuration

### 9.1 Environment variables

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `LLMUX_PROXY_KEY` | Yes | None | Shared client bearer key |
| `LLMUX_ACCOUNT_K1_KEY` | Yes | None | Account `k1` upstream key |
| `LLMUX_ACCOUNT_K2_KEY` | Yes | None | Account `k2` upstream key |
| `LLMUX_ACCOUNT_K3_KEY` | Yes | None | Account `k3` upstream key |
| `LLMUX_AFFINITY_HMAC_KEY` | Yes | None | Independent key for durable session digests |
| `LLMUX_DB_PATH` | Yes | None | Absolute SQLite path |
| `LLMUX_LISTEN_ADDR` | No | `127.0.0.1:4000` | HTTP listen address |
| `LLMUX_LOG_LEVEL` | No | `info` | Process log threshold |

The three account keys must be non-empty and distinct. Duplicate credentials would create separate limiter buckets for one real account and are therefore a fatal configuration error.

The affinity key must be at least 32 bytes, the same floor the proxy key carries and for a related reason: a key shorter than the digest it feeds weakens the one property that digest exists for, which is that a stored `session_key` says nothing about the header that produced it. The proxy and affinity keys must be distinct from each other and from every account key. The affinity key is separate from the proxy key rather than derived from it, so that rotating the client credential does not silently invalidate every stored digest and lose an hour of affinity for every live conversation. It has no default and cannot be generated per boot: either would make recovered pins unmatchable and quietly disable the restart recovery §16.3 promises.

The store’s path variable names a database and not a log, because the distinction it sits on is the one this document works hardest to keep: process logs are ephemeral, go to stderr and may be rotated freely, while the durable store is append-only evidence that §15.10 forbids truncating and no retention tooling should ever be pointed at. A variable called a log path invites exactly that. `LLMUX_LOG_LEVEL` keeps its name because it genuinely is about process logs.

Key changes require restart. There is no reload endpoint, signal-based reload, watcher, or mutable configuration file.

### 9.2 Fixed policy constants

| Concern | Value |
| --- | ---: |
| Logical request deadline | 10 minutes |
| Shutdown grace after the first signal | 10 minutes 10 seconds |
| Maximum account-acquisition time per attempt | 60 seconds |
| Maximum request body | 64 MiB |
| Non-streaming precommit response buffer | 8 MiB |
| Maximum JSON nesting depth | 256 |
| Aggregate request/replay/precommit memory budget | 512 MiB |
| Unknown-length body charge step | 1 MiB |
| Concurrent admitted chat requests | 128 |
| Live accepted client connections | 256 |
| Global request-admission wait | 1 second |
| Session affinity TTL | 1 hour |
| Live session pins | 4096 |
| Provisional pin maximum lifetime | Logical request deadline |
| Saturated-pin grace | 5 seconds |
| Rolling rate window | 60 seconds |
| Post-start dispatch blackout | 60 seconds |
| Dispatches per window/account | 60 |
| In-flight attempts/account | 12 |
| Maximum dispatches/logical request | 4 |
| Minimum deadline runway before a retry dispatch | 5 seconds |
| Intermediate response drain cap | 64 KiB |
| SSE observer line cap | 1 MiB |
| Observer cumulative decoded-output cap | 64 MiB per response |
| SQLite busy timeout | 5 seconds |
| Store-operation ceiling | 6 seconds |
| Passive checkpoint interval | 256 terminal commits |
| WAL size warning threshold | 64 MiB |
| Server header-read timeout | 5 seconds |
| Server request-read timeout | 2 minutes |
| Server idle timeout | 2 minutes |
| Downstream write deadline, armed per write | 30 seconds |
| Maximum request headers | 64 KiB |
| Maximum upstream response headers | 128 KiB |
| Upstream dial timeout | 10 seconds |
| Upstream TLS handshake timeout | 10 seconds |
| Upstream idle-connection timeout | 90 seconds |

These are implementation constants, not a generic tuning surface.

The aggregate budget and the concurrent-request ceiling stay constants for the same reason as the rest, even though a host-dependent number is the obvious candidate for an environment variable. This process runs on one known machine, `GOMEMLIMIT` is already the host-level control and the Go runtime reads it without help, and a second memory knob would only make it possible to configure the two into disagreement. The runbook recommends a budget no larger than 60% of `GOMEMLIMIT`, leaving headroom for the runtime, SQLite, goroutine stacks, and transport buffers.

The two per-account ceilings are the exception, and the reason is worth stating because it changes what they are for. Neither 60 nor 12 is a published Ollama Cloud limit; no such limit is documented. They are self-imposed guards, and the earlier pair of 25 and 3 was a guess that had hardened into a specification. A guard set below the real ceiling is indistinguishable from the real ceiling, so it can never be measured, and the proxy spends capacity it has already paid for.

Upstream 429 is the only authoritative statement of the real limit, and the design reacts to it rather than trying to prevent it: 429 is retried, `Retry-After` is honoured, repeated 429 within a window cools the account, and every one of those events is a row in the attempt log.

What that log can settle runs in one direction only. Sustained 429s under the local ceiling prove the ceiling is set too high and by roughly how much. Silence proves nothing: a ceiling of 60 cannot discover whether upstream would have accepted 100, because it is the thing preventing the dispatches that would answer the question. The ceilings are therefore safety and resource policy, chosen high enough to stop being the binding constraint at the traffic these three consumers produce, and revised downward from evidence rather than upward from hope. They are expected to be re-derived from success, latency, 429 and saturation data after real traffic, which is the one tuning this document invites.

That invitation has an owner and a gate, because an invitation with neither is a constant nobody ever revisits. The owner is the post-cutover review of §30.10, which is also where the cooldown constants of §20.2 are re-derived, and the gate is stated there. Until that review has produced its decision, the two numbers above are what the implementation enforces and what its tests assert; they are not provisional in the sense §8.1 uses for the upstream model strings, which no implementation may be built on at all.

The in-flight ceiling keeps a second job that survives the change: it bounds concurrent upstream work, since every live attempt holds its replay body and, for a non-streaming response, up to an 8 MiB precommit buffer. Twelve per account is also roughly the concurrency that 60 dispatches per minute implies at the latencies these callers see, so the two numbers are no longer independent guesses. It does not bound memory on its own, because a request buffers its body long before it reaches an account; that is the aggregate gate’s job.

### 9.3 Secret delivery and process launch

The binary reads configuration from the environment, but the operating documentation must define a safe way to supply that environment:

- Do not put proxy or account keys in command-line flags, checked-in unit files, shell history, or example files.
- For a service manager, use an owner-readable environment file outside the repository, mode `0600`, referenced by the user service.
- The example environment file committed to the repository contains names and placeholders only.
- The reference service definition disables core dumps and sets an owner-only umask, since a core file of this process contains four credentials and whatever prompt text was in flight.
- The database directory should be owned by the user and should not grant write access to other users.
- The service definition should set `GOMEMLIMIT` above the aggregate memory budget with room to spare, since the budget covers only request-owned buffers.
- Startup messages may name a missing variable but must never print its value.
- Diagnostic commands in the runbook must not dump the process environment.
- Key changes require restart. Restart is the only key-reload mechanism.

The repository should include a reference user-service definition and an environment-file template, but the binary must not depend on systemd or any particular supervisor.

## 10. Architecture

### 10.1 Architectural style

Use a small layered service with explicit construction. The problem warrants separation for concurrency, HTTP relay, and persistence, but not a generic provider framework or enterprise layering.

Dependency direction:

`cmd/llmux` → application composition → HTTP proxy, route coordinator, upstream client, durable store.

Lower-level components do not import the application or command package.

### 10.2 Repository layout

| Path | Responsibility |
| --- | --- |
| `cmd/llmux` | Minimal entry point, the `version` and read-only `db` subcommands, configuration load, construction, run, exit status |
| `internal/app` | Composition root, server lifecycle, startup recovery, signal handling |
| `internal/catalog` | Fixed routes, generated pinned variants, model-list projection |
| `internal/rewrite` | Top-level JSON scanner, rewrite plan, immutable replay segments |
| `internal/proxy` | Auth, handlers, retry loop, headers, relay commitment, usage observation |
| `internal/route` | Account limiter, health, session affinity, account selection, leases |
| `internal/resource` | Global handler slots and the weighted aggregate-memory gate |
| `internal/store` | SQLite configuration, migrations, inserts, startup recovery queries |
| `internal/idgen` | Proxy-owned random identifiers |
| `internal/testsupport` | Test-only clock/timer control, scripted upstream, deterministic shuffler, raw HTTP client helpers |
| `deploy` | Reference user-service definition and placeholder-only environment template |

The request scanner and rewriter sit apart from `internal/proxy` because they are the most heavily specified and most heavily tested component in this document and have no HTTP dependency whatsoever. Separating them makes the byte-preservation contract one package’s public API, lets the fuzz targets of §28.3 build without server plumbing, and removes the seam along which relay code would otherwise start reaching into scanner internals. It is the cohesion argument already made for `internal/resource` and `internal/idgen`, applied to a larger unit. Response bodies remain in `internal/proxy`, which is why the package is named for the rewrite rather than for bodies in general.

There is no `pkg`, `utils`, `helpers`, provider registry, plugin directory, generated router, or ORM model layer.

### 10.3 Dependency injection

Use manual constructor injection.

- No mutable service globals.
- No `init`-time resource creation.
- Constructors return concrete types.
- Small interfaces are defined where consumed only when needed for tests.
- Likely test boundaries:
  - Clock and timer creation
  - Permutation source
  - Upstream HTTP executor
  - Append-only transaction writer
- The composition root alone knows every concrete dependency.

The clock abstraction must cover UTC wall time, monotonic time and cancellable timer creation, and it must expose the two clocks as separate reads a test can advance independently. Every clock rule in §24.6 is a statement about the difference between them, so a boundary that moves them together can express none of the anomalies this document specifies against, and a test written against such a boundary cannot tell which clock the implementation read. Tests must not combine a fake `Now` with real timers, because that creates impossible scheduler states. Go 1.26 `testing/synctest` should be used where exercising the real timer/context machinery is clearer than a fully injected clock.

### 10.4 Core components

#### Application

Owns:

- HTTP server.
- Shared upstream transport/client.
- Route coordinator.
- SQLite durable store.
- Structured logger.
- Shutdown state.

#### HTTP proxy

Owns:

- Authentication.
- Exact endpoint behavior.
- Ten-minute logical request context.
- Body reading and rewriting.
- Account lease acquisition.
- Retry state machine.
- Staged response headers that remain uncommitted until relay policy permits commitment.
- Streaming first-read priming and non-streaming bounded precommit buffering.
- Explicit committed-stream abort semantics.
- Final response commitment and relay.
- Attempt-record construction.

#### Route coordinator

Owns:

- All three account states.
- All live session pins.
- Rate timestamps.
- In-flight counts.
- Account health.
- Randomized candidate selection.
- Wait notifications.
- The post-start dispatch blackout deadline, which blocks every account at once.
- Spill and re-pin decisions.

It starts no background goroutine.

#### Resource gate

Owns the two process-wide admission bounds that no per-request limit can provide.

- A counting semaphore over concurrent admitted chat handlers.
- A weighted budget over request-owned memory, charged in bytes and released on every exit path.
- One bounded acquisition wait, after which the caller receives local 429 rather than queueing.
- A listener wrapper bounding live accepted client connections.

Both are acquired before the body is read and released by the same defer that releases the handler slot. The precommit allowance is a later charge against the same budget, taken only by a response that is actually going to buffer and released by that same defer. The gate holds no coordinator state, makes no I/O, and knows nothing about accounts: it bounds what the process has allocated, while the coordinator bounds what upstream is asked to do. Neither substitutes for the other, because a request buffers its body long before it competes for an account and may never reach one at all.

The handler semaphore bounds handlers and not the goroutines waiting to become one. `net/http` creates a goroutine per accepted connection and reads the request on it before any handler runs, so a caller that opens connections faster than they complete grows that population whatever the semaphore admits, and the per-connection read buffers grow with it. The only place that can be bounded is where connections are accepted, which is why the listener is wrapped: past the ceiling an accept waits for a live connection to close. Three local consumers will never approach it, and a consumer defect that leaks connections is what it is for. Without it the controls in §26.6 bound everything except the one goroutine per connection that precedes all of them.

#### Upstream transport

Uses one shared transport for all accounts. Credentials are per-request headers, so pooled connections need not be account-specific.

Properties:

- Automatic response decompression disabled.
- Redirect following disabled.
- Environment proxy discovery disabled by setting `Proxy` to nil. Go’s default transport reads `HTTPS_PROXY` and `ALL_PROXY`, which would route three account credentials through whatever host an environment variable named, for a destination this document fixes in source.
- Certificate verification enabled, minimum TLS 1.2.
- HTTP/1.1 and HTTP/2 enabled explicitly over TLS rather than left to defaults.
- `MaxConnsPerHost`, `MaxIdleConns`, and `MaxIdleConnsPerHost` set at or above the 36 concurrent attempts three accounts of twelve permit. Go defaults to two idle connections per host, which would make most dispatches pay a fresh TLS handshake.
- `MaxResponseHeaderBytes` bounded to 128 KiB rather than the ten-megabyte default.
- `ResponseHeaderTimeout` deliberately zero. A queued or slow-starting generation is not a failure, and the logical deadline is the only bound this design wants on the wait for headers.
- Bounded dialing and TLS setup.
- Overall lifetime controlled by request context.
- Idle connections closed at shutdown.

#### Durable store

- One writer connection for admissions, phase batches, lifecycle rows, and local-rejection rows.
- One maintenance connection used only for checkpoints and read-only checks, which writes nothing.
- Parameterized handwritten SQL.
- Synchronous append of one dispatch-admission row before every `http.Client.Do`.
- Synchronous transactional batch inserts.
- One phase batch contains its deduplicated selection skips followed by either its dispatched attempt or terminal selection failure.
- One row per authenticated chat request that receives a local response before account selection begins.
- One lifecycle row at startup and one at shutdown.
- Startup-only recovery queries.
- No ORM or database server.

## 11. Application lifecycle

### 11.1 Startup

1. Load configuration.
2. Validate required values without logging secrets.
3. Ensure account keys are distinct.
4. Validate the fixed route catalog.
5. Validate the database path, its parent directory, and any existing file's type and permissions, as §15.2 requires.
6. Acquire an exclusive advisory lock on `<LLMUX_DB_PATH>.lock`, creating it inside that validated directory with mode `0600` if absent, and hold it for the process lifetime. Failing to acquire it is fatal. The read-only `db` subcommands never take it, so inspection and backup stay possible while the proxy serves.
7. Open SQLite at the exact configured path, and set and read back the required connection-local pragmas on every connection it opens.
8. Apply embedded forward-only migrations.
9. Verify append permissions using a transaction that is rolled back.
10. Arm the post-start dispatch blackout from the process's monotonic origin. No rate state is read from the store.
11. Recover unexpired successful session pins.
12. Append a `process_start` row.
13. Construct the route coordinator.
14. Construct the upstream transport and HTTP handlers.
15. Bind the configured socket through the connection-limiting listener.
16. Announce readiness only after the socket is bound.
17. Serve until termination or fatal server failure.

Startup fails if:

- Configuration is invalid.
- Catalog validation fails.
- The store lock is held by another live process.
- The database cannot be opened.
- Schema version is unsupported.
- Migration fails.
- The database is not writable.
- The listener cannot bind.

There is no degraded startup mode.

### 11.2 Graceful shutdown

1. On SIGINT/SIGTERM, mark the application as draining.
2. Stop accepting new requests.
3. Do not cancel handler contexts on the first signal. Let active handlers finish within the remainder of their request deadline, capped by ten minutes.
4. A second signal, or expiry of the shutdown grace of §9.2, cancels the application force context and calls `Server.Close`. That grace exceeds the logical request deadline by more than the store-operation ceiling on purpose. A handler admitted an instant before the signal may run for the full deadline and then needs its bounded store context to write the terminal row, and §12 step 26 derives that context from the force context, so a grace equal to the deadline would cancel exactly the record a graceful shutdown exists to flush.
5. Handler cleanup releases account leases.
6. Handler cleanup appends terminal rows where possible.
7. Close idle upstream connections.
8. Append a `process_stop` row.
9. Attempt one final passive checkpoint on the maintenance connection.
10. Close SQLite last.
11. Return zero for orderly signal-driven shutdown.
12. Return nonzero for startup failure, unexpected serve failure, or forced incomplete shutdown.

No periodic health, cleanup, checkpoint, vacuum, or model-discovery worker is added. The passive checkpoint attempts in §15.2 still run in the foreground of a terminal commit that is already happening, which is why they are not one; what changed is which connection they run on, not who drives them or when. Step 9 above is the same attempt driven by the last event of the lifecycle rather than by a commit, and it is what stops a WAL left short of the commit interval from being inherited across every restart, now that nothing checkpoints automatically.

## 12. Request data flow

1. Match exact path and method.
2. Authenticate.
3. Generate a proxy logical-request ID.
4. Create a context ending no later than ten minutes after admission.
5. Acquire a global handler slot within one second, or return local 429 `proxy_overloaded`.
6. Charge the aggregate memory gate for the body before reading it, in allocated capacity rather than in bytes received. A valid `Content-Length` charges its rounded allowance once. A body whose size is unknown charges the fixed initial step and extends its charge one step at a time, always staying ahead of the capacity allocated, so every growth of the backing array is charged before it happens; a denied extension releases everything the request holds and returns local 429 `proxy_overloaded`. When the read completes the charge settles to the buffer’s capacity and not to its length, because capacity is what the process holds for the rest of the request. Charging after the read would bound nothing; charging the 64 MiB worst case for the lifetime of every chunked request would let eight small uploads exhaust the entire budget between them while the process was holding a few hundred kilobytes; and charging length against a geometrically grown buffer would under-count the budget by the slack that growth left, which under the usual doubling is most of the final step.
7. Read a known-length body into one exact allocation. A body of unknown length grows inside its charge instead, because a single exact allocation is what a declared length buys and nothing else can produce it.
8. Scan the top-level routing fields and build the immutable segmented replay plan.
9. Do not reserve the precommit allowance here. It is charged only once a final response is classified as non-streaming 2xx, immediately before its buffer is allocated.
10. Resolve the route catalog entry.
11. Read the optional session ID, reject an oversized one, and reduce it to its digest.
12. Build and validate the immutable upstream request template before reserving account capacity. This includes the fixed URL, the segmented replay plan, and allowed client headers, but not the account credential.
13. Start an account-selection phase whose deadline is the earlier of 60 seconds and the logical request deadline.
14. Ask the coordinator for an account lease. Collect distinct skip observations in bounded request memory after releasing the coordinator lock; do not write SQLite while selection is still changing.
15. If selection terminates without a lease:
    - Compute `Retry-After` when at least one account has temporary capacity state.
    - Transactionally append the deduplicated skip rows and one `selection_failure` row.
    - Return local 429 for temporary capacity exhaustion or local 503 when no flexible account is usable.
16. If selection succeeds, install release cleanup immediately. The lease holds a pending reservation: it occupies one RPM slot and one in-flight slot from this moment, and no dispatch timestamp exists for it yet.
17. Generate the attempt’s `attempt_id` and synchronously commit its `dispatch_admission` row, inside the bounded store-operation ceiling.
18. If the admission commit fails:
    - Cancel the pending reservation, which frees the RPM slot it was holding and releases its in-flight slot.
    - Append no dispatch row, because no dispatch occurred.
    - Return local 503 `admission_store_unavailable`.
19. Reacquire the coordinator mutex, convert the pending reservation into a dispatch timestamp at the current instant, unlock, and call `http.Client.Do` immediately. That second critical section touches memory only and has no failure branch: the admission is committed, so the attempt is going upstream whatever the mutex reveals. The admission commit is the only fallible step permitted between reservation and dispatch, and it ends the attempt rather than being worked around: account authorization and request-context binding are the only other work left, and neither can fail. Once the admission commits, its slot is never refunded, even if a local panic or transport failure prevents a provably completed send. A crash after the commit but before `Do` leaves a phantom admission, a row the log reads as an attempt whose ending nothing recorded. The rolling ceiling does not rest on that ambiguity: §16.3 carries it across a restart on the monotonic clock rather than by reading the ledger back.
20. Classify transport errors and upstream status before writing any downstream status or headers.
21. If retrying:
    - Update account health from the classified status, under the coordinator lock, before anything else.
    - Drain and close the intermediate response within bounds.
    - Release the lease.
    - Transactionally append the selection skips and attempt row.
    - Wait using context-aware backoff.
    - Begin the next selection phase with the next prospective attempt number.
22. If final:
    - Stage filtered upstream headers without committing them.
    - For SSE, successfully read the first non-empty upstream body chunk before downstream commitment.
    - For non-streaming 2xx, charge the gate for the 8 MiB precommit allowance and buffer up to that bound before commitment; complete small bodies in memory and transition larger bodies to progressive relay. A denied charge skips the precommit phase and relays progressively from the first byte, which is the path an oversized body already takes.
    - For a body buffered in full inside the precommit bound, close the upstream response and release the account lease before committing, because no live upstream attempt remains.
    - Commit status and headers exactly once.
    - Relay the exact staged and subsequent response bytes.
    - Observe usage and time to first event without retaining content.
    - Close the response and release the account lease, unless the buffered path already did.
    - Update health and session state.
    - Transactionally append the selection skips and final attempt row.
23. Release the body buffer and its memory charge as soon as no further replay can occur, which is the point at which the last attempt becomes terminal. There is no separate rewritten body to release, and no body is written to disk.
24. Release the global handler slot and any remaining memory charge on every exit path, including panic and cancellation.
25. A request that ended before step 13 began writes its local result to the client and then appends one `unrouted_request` row, which is the only durable record such a request produces. A request whose client vanished before any status was written appends nothing, which §15.3 states as a decision rather than an omission.
26. Terminal persistence uses a bounded context derived from the application force-shutdown context, not the client request context and not the expired logical-request context. A client that disconnects, or a deadline that expires, must not cancel the write that records exactly that.
27. Return only after the terminal transaction has been attempted.

## 13. Upstream request and response handling

### 13.1 Request headers

A fresh upstream request is built for each attempt.

Copy only this fixed end-to-end request-header allowlist when present:

- `Content-Type`
- `Accept`
- `Accept-Encoding`
- `User-Agent`

Set or derive:

- `Authorization: Bearer <selected account key>`.
- Correct `Content-Length` for the replayed segments.
- The fixed upstream host and chat-completions path.
- An explicitly empty `User-Agent` when the client sent none. `net/http.Transport` otherwise synthesizes its own default, so the narrow allowlist would be leaking this process’s runtime to upstream through a header the client never set.
- No `GetBody` on the outbound request, and no idempotency header. Either would let the transport transparently replay this POST, and a replay it performed would be an upstream dispatch with no admission row and no RPM slot, breaking §4 invariants 10 and 11 underneath every layer that does the accounting.

Never forward:

- The client’s proxy `Authorization`.
- `X-Session-ID`.
- `Host`.
- `Cookie`.
- `X-Forwarded-*` or proxy headers.
- Trace/correlation headers not in the allowlist.
- `Content-Encoding`, because compressed request bodies are rejected before rewrite.
- `Connection`, `Keep-Alive`, `Transfer-Encoding`, upgrade headers, or any header named by `Connection`.

The narrow allowlist is deliberate. The fixed consumers need body compatibility, not an arbitrary-header tunnel, and it prevents accidental credential or machine-metadata leakage.

A drop is never silent. Each attempt records the count of request headers the allowlist removed, and a debug-level structured event names them:

- Only header names are emitted, never values, because a value is where a cookie or a credential lives.
- Names are emitted to the process log, never to the attempt store, whose schema has no header column.
- The count alone is enough to notice that a consumer began sending something; the names are what identify it.
- A consumer that turns out to need a dropped header changes this list, which is a deliberate edit, rather than discovering the loss as unexplained upstream behavior.

Both resources reject a non-empty query string with local 400. The OpenAI chat contract carries everything in the body, no consumer sends one, and forwarding client-controlled query text to the upstream widens its surface for nothing. Silently dropping it instead would be worse than either: a caller that believed a parameter meant something would never learn otherwise, which is the class of quiet corruption this proxy exists to remove.

Request trailers are unsupported because the JSON body is fully buffered.

### 13.2 Redirects

Redirect following is disabled.

An upstream 3xx or 101 is an unexpected protocol response rather than a result:

- It is never followed.
- It is never relayed, and neither `Location` nor `Upgrade` nor any other connection-routing metadata reaches the client. Relaying one hands a local consumer an instruction to re-send, carrying the proxy bearer key, to a host chosen upstream.
- Before downstream commitment it becomes local 502 `invalid_upstream_response`.
- It is non-retryable, because a protocol or configuration fault does not improve on another account.

### 13.3 Final response preservation

For the final upstream response:

- Relay exact status.
- Copy all end-to-end headers.
- Strip hop-by-hop connection/framing headers and every header named by `Connection`.
- Strip `Alt-Svc`, `Set-Cookie`, `Clear-Site-Data`, `Refresh`, and `Proxy-Authenticate`. These do not describe the completion, they instruct the recipient about where and how to make its next request, and the recipient here is a local consumer holding the proxy key whose origin is this proxy rather than upstream.
- Relay exact body bytes.
- Preserve compression when explicitly used.
- Preserve application trailers when supported, declaring the allowed trailer names before commitment and filling their values only after upstream EOF.
- Validate the declared trailer names against the same framing, connection, routing, cookie, authentication, and proxy-owned rules as ordinary response headers, and treat an invalid declaration as an invalid upstream response before commitment. Otherwise the trailer field is a channel around every header rule above it, and the declaration arrives before commitment precisely so that the judgement is still available.
- Do not parse or rewrite completion JSON or SSE events.
- Forward upstream-generated IDs but do not store them.
- Replace any upstream `X-LLMux-Request-ID` with the proxy logical request ID.

The strip list is short and closed for the same reason the request allowlist is narrow. Everything on it is a state or routing directive scoped to an origin that the proxy, not upstream, owns.

Application-level “unchanged” means unchanged status, end-to-end headers, and body bytes. TCP segmentation, chunk boundaries, and hop-by-hop framing are naturally regenerated by `net/http`.

### 13.4 Streaming

Streaming relay is selected when either of these holds:

- The unique raw top-level request `stream` value is exactly `true`.
- The upstream media type parses as `text/event-stream`.

Recognizing it from the upstream content type alone was one signal short. A request that asked for a stream and came back labelled something else would be buffered toward the 8 MiB precommit bound, so the client’s first token would arrive when the generation finished rather than when it started, and nothing would report why. The request’s own `stream` value is still never validated and never altered; it selects relay behavior and nothing more.

A disagreement between the two signals is emitted as a debug-level process event, by classification only and with no content, in the same shape as the dropped-header event. It never changes a single response byte.

For SSE:

- Stage status and filtered headers without writing them to the downstream connection.
- Perform a first-read primer against the upstream body.
- Zero-length reads with no error are retried without committing.
- EOF or a read failure before any body byte is available remains uncommitted and is eligible for the response-read retry policy.
- Once a non-empty chunk is available, commit the staged status and headers, write that exact chunk, and flush.
- Flush after each successfully written upstream chunk.
- Never buffer the complete response.
- Preserve comments, event fields, blank lines, `[DONE]`, ordering, and whitespace.
- Backpressure from the client naturally slows upstream reads.
- Arm a 30-second write deadline through `http.ResponseController` before each write and flush, and clear it once the call returns. Backpressure is a healthy client reading slowly; a client that has stopped reading entirely is indistinguishable from it at the API and would otherwise block the handler, its lease, and its upstream connection until TCP gave up.
- A downstream write that exceeds that deadline is a client-side transport failure, handled exactly like any other downstream write failure.
- A downstream write failure cancels the upstream request and closes its body.
- An upstream read failure after commitment is never followed by a retry.
- If upstream reading fails after commitment while the client is still connected, terminate the downstream HTTP response with the server’s abort sentinel rather than returning a clean EOF.
- A normal EOF after a chunk is relayed unchanged. Absence of `[DONE]` may be recorded as truncation metadata, but the proxy does not fabricate the marker.

### 13.5 Non-streaming responses

For a final non-streaming 2xx response:

1. Stage status and filtered headers without committing them.
2. Charge the aggregate memory gate for the 8 MiB precommit allowance and read into a bounded buffer of that size.
3. If EOF arrives within the bound:
   - Treat the response as complete.
   - Extract a complete usage object from the buffered bytes.
   - Close the upstream body and release the account lease.
   - Commit the upstream status and headers.
   - Write the original buffered bytes exactly once.
4. If the body exceeds 8 MiB:
   - Commit status and headers.
   - Write the already-read prefix exactly.
   - Continue progressive unchanged relay through the bounded usage observer.
5. If the gate denies the precommit allowance, commit status and headers at once and relay progressively through the bounded usage observer, exactly as an oversized body does.
6. If reading fails before commitment, classify the failure and retry when its budget allows.
7. If reading fails after commitment, record truncation and abort the response.

The allowance is charged here rather than at admission, which is where a per-request reservation would naturally sit. Charged early it is charged for every request, including every stream and every request still waiting for an account or backing off between attempts, and 128 concurrent handlers holding 8 MiB apiece is 1 GiB against a 512 MiB budget. The gate would spend most of that budget on a buffer most requests never allocate, and the concurrency ceiling would quietly become a memory ceiling of roughly half its stated value. Charging once the response is known to be a non-streaming 2xx means only the requests that use it hold it. What that moves is a possible denial from admission time to relay time, and the answer to a denial already exists: the body relays progressively. That costs the precommit retryability of §29.17 and nothing else, because the bytes the client receives are identical on both paths.

The lease goes back at upstream EOF and not after the downstream write, because by then the upstream attempt is finished and its connection closed. The in-flight ceiling bounds live upstream work; letting it follow a client that drains eight megabytes slowly would spend account concurrency on a transfer upstream is no longer part of.

This buffering does not persist completion text. It is request-lifetime process memory and is released immediately after relay. It is bounded independently of the request-body buffer.

Final non-authentication 4xx responses and exhausted 5xx responses may be committed and relayed without waiting for a complete 8 MiB precommit read; their upstream status is already the final client-visible result. An upstream 3xx or 101 never reaches this path, because §13.2 turns it into a local 502 before commitment, and neither does a 401, because §20.1 fails the request over to another eligible account or answers a local 502 rather than relaying one. A later body failure is recorded and the response is aborted rather than rewritten.

### 13.6 Response commitment

The response is considered committed once its status has been written or body bytes have caused an implicit status write.

Copying headers into an in-memory staged header set is not commitment. The downstream `ResponseWriter` must not be mutated until the handler has irrevocably chosen that upstream response.

After commitment:

- No retry.
- No local JSON error replacement.
- No second status.
- No concatenated response body.
- No attempt to hide truncation.
- No synthetic SSE error or `[DONE]` event.

The terminal attempt row records whether commitment occurred.

For an upstream read failure after commitment:

- Release the lease and build the terminal attempt record.
- Attempt the synchronous log transaction.
- Abort the HTTP response using `http.ErrAbortHandler` or the equivalent protocol-aware server mechanism.
- The top-level panic recovery boundary must recognize and re-propagate the abort sentinel. It must not turn it into a local 500 or emit a misleading stack trace.
- The client must observe a closed and truncated HTTP/1.x response. That is the only case, because the local server speaks HTTP/1.x only.

A downstream write error means the client is already gone. In that case, cancel upstream, release and log, then return without a second abort.

### 13.7 Timeout semantics

- Ten minutes is a hard deadline for the complete logical request, including account acquisition, upstream attempts, backoff, and relay.
- Every retry inherits the same logical context and only the remaining time. No retry receives a fresh ten-minute budget.
- Client cancellation wins immediately. HTTP exposes no caller-side deadline other than a cancellation that actually reaches the server, so there is nothing else to honour.
- Dial and TLS-handshake timeouts can fail early enough to permit retry.
- Expiry of the overall logical deadline is terminal and is never itself retried.
- The two deadlines are not interchangeable and do not produce the same answer. The 60-second acquisition ceiling answers local 429 with `Retry-After`, because capacity is expected to return and the caller is being told when to come back. The ten-minute logical deadline answers local 504 before commitment, because the request itself expired. Client cancellation produces no response at all.
- There is no separate two-minute stream-stall timeout. A reasoning model may legitimately remain silent, and the ten-minute logical deadline already bounds the resource. Adding a shorter idle timer would turn valid slow generations into false failures.
- The server’s absolute `WriteTimeout` remains disabled because it is incompatible with valid long-lived streams.
- `ReadTimeout` is two minutes and bounds receipt of the complete request body. `ReadHeaderTimeout` bounds only the headers, so without it a client that trickles a body holds a handler and its buffered body for as long as it likes: neither the logical context nor client cancellation interrupts a body read, because a slow client has not disconnected.
- A downstream write deadline is armed immediately before each write or flush and cleared as soon as that call returns. It bounds the write, never the wait for upstream, so a model that produces nothing for five minutes is untouched by it while a consumer that has stopped reading is not.

## 14. Response observation

### 14.1 First-event definition

For successful SSE responses whose bytes the observer can read, directly or through the bounded decoder of §14.3, `time_to_first_event` is operationally defined as:

- The invocation of `http.Client.Do`, through
- Recognition of the first complete non-empty SSE `data:` event other than `[DONE]`.

This definition excludes:

- Response headers.
- Empty keepalive lines.
- SSE comments.
- `[DONE]`.

It does not retain the event’s content.

The anchor is the `Do` invocation rather than the dispatch reservation, because §12 places the admission commit between the two. Measured from the reservation, every first event would carry the store’s commit latency inside a number named for upstream behavior, and the weekly ceiling re-derivation of §30.10 would be tuned partly against this machine’s filesystem. `attempt_duration_us` shares that anchor for the same reason, and so does the terminal row’s own `event_at_us`. The reservation instant keeps a column of its own, `dispatch_admission.reserved_at_us`, on the row that is written at that boundary and can hold nothing else. The terminal row is written after the call returns, so it can and does record the instant the call was made, which is what §30.3 counts dispatches by and what §30.10 re-derives the ceilings from. The window itself is measured at the same boundary, by §25 invariant 31.

It is deliberately not called time to first token. In OpenAI-shaped streams the first event routinely carries a role declaration or other protocol metadata and no generated text at all, so the number is a latency-to-first-byte-of-stream measurement wearing a token’s name. Naming it accurately costs nothing now and prevents a permanently mislabeled column, which a schema this document forbids updating cannot fix later without a migration.

For non-streaming responses, and for any response whose content encoding the bounded observer cannot decode, `time_to_first_event` is `NULL`. Total attempt duration remains available.

### 14.2 Token counts

Persist only complete, non-negative upstream-reported values:

- `usage.prompt_tokens`
- `usage.completion_tokens`
- `usage.total_tokens`

Rules:

- Never estimate counts.
- Never run a tokenizer.
- Never compute cache tokens.
- Never sum retry attempts.
- Missing or malformed values remain `NULL`.
- If several complete usage objects appear, the last complete one wins.
- Only a top-level response `usage` object is read. A string or a nested application value that happens to be named `usage` is ignored.
- Partial usage data is not persisted as if complete.
- A disconnect or truncated stream generally leaves counts `NULL`.
- The proxy never modifies the request to force usage reporting.
- A streaming client may explicitly request usage; that client-owned field crosses unchanged.

### 14.3 Observer bounds

The observer must not retain unbounded response text.

- A complete non-streaming body within the 8 MiB precommit bound is parsed from that already-required buffer using a narrow usage projection, then discarded.
- A larger non-streaming body is observed incrementally after transition to progressive relay.
- SSE frames are observed incrementally while the original bytes are relayed.
- The relayed bytes are never decompressed, re-encoded, or altered by observation, and automatic transport decompression stays off. Whatever encoding upstream chose reaches the client exactly as it arrived.
- For a response whose `Content-Encoding` is exactly `gzip`, the observer feeds a copy of the relayed bytes through a bounded streaming decoder from the standard library and reads the decoded output exactly as it reads an identity body. Decoded output carries the same caps as identity observation plus a cumulative decoded-output cap, which is what bounds the work a degenerate or hostile response can demand. Exceeding any cap abandons observation for that response and never touches relay.
- The decoder pulls and the relay pushes, so the copy crosses a bounded buffer between them. The observer never delays a downstream write: when that buffer is full the bytes that would have entered it are dropped and observation is abandoned for the response. Whatever bridges the two is created with the request and torn down with it, and abandoning is an ordinary outcome of that path rather than a failure.
- Any other content encoding, including a multi-valued one, and any decoder error, disables semantic observation for that response. Exact relay continues either way, and both token counts and first-event timing are `NULL`.
- Under progressive relay, whether SSE or oversized non-streaming, the observer consumes chunks after or alongside successful downstream writes, so observation can never delay or reorder relay.
- JSON strings and unrelated values are skipped rather than copied into a second response-sized structure.
- SSE parsing keeps at most a 1 MiB line buffer.
- If a line exceeds the cap, relay continues unchanged and semantic observation for that line is abandoned.
- Observer failure never changes response bytes.
- Parser panics are prohibited and fuzz-tested.

The two decoder bullets above are conditional, and this is where that is marked rather than only argued. They ship only if §31 Phase 0 records upstream selecting a compressed encoding when it is sent a real consumer’s `Accept-Encoding`, and Phase 6 delivers the decoder and its bounded bridge on that record alone. If Phase 0 records identity throughout, neither is built, and every non-identity encoding is then handled by the bullet that already covers the rest: exact relay continues, and both token counts and first-event timing stay `NULL`. Nothing else in this section is conditional; the caps, the abandonment rules and the prohibition on delaying a write hold for identity observation whatever Phase 0 finds.

The decoder exists because without it the evidence the whole of §30 rests on can quietly go to zero. `Accept-Encoding` crosses to upstream in the request allowlist, and mainstream HTTP stacks advertise compression by default, so if upstream honours what a consumer sends then token counts, usage and first-event timing are `NULL` for every request that consumer makes, and the weekly ceiling re-derivation loses its volume and latency signal without anything reporting a failure. Dropping `Accept-Encoding` from the allowlist is the cheaper fix and is rejected: it spends real bandwidth on every completion to solve a problem on the observer’s side of the process, and it mutates upstream-visible negotiation on behalf of a client that asked for something else. Whether the decoder is needed at all is a question about upstream rather than a question of design, so the Phase 0 gate measures which encoding upstream actually selects for each consumer’s real `Accept-Encoding`, and that measurement decides whether the decoder ships. Timing observed through the decoder inherits upstream’s own flush boundaries, which are the boundaries the client sees too, so a first-event figure stays comparable across encodings.

Forcing `Accept-Encoding: identity` upstream and dropping the header from the request allowlist would delete the decoder, its bridge, and the decompression-bomb path in one move, and it remains rejected. It alters what the client asked for so that the proxy can read the answer more easily, which §30.7 states as a general principle in the other direction: the proxy will not alter a request or a response to improve its own observability, and what the callers send is therefore what decides what the log can hold. It also spends real bandwidth on every completion, permanently, to solve something on the observer’s side of the process. What it removes is conditional, since the decoder ships only if the Phase 0 gate finds upstream selecting a compressed encoding at all, so the trade is a bounded buffer and one lifecycle against a stated principle and a standing cost.

### 14.4 Upstream rate-limit headers

§9.2 concedes that the local ceilings cannot be measured from below: the guard prevents exactly the dispatches that would locate the wall, so the evidence about it runs in one direction only. Numeric rate-limit response headers are the one exception upstream can offer, because they arrive on successes rather than on refusals, and an observed remaining-quota floor across a week is the measured distance between the guard and the wall, which is the direction 429 silence never bounds.

Whether they exist is a question about upstream and not a design choice, so it is settled where the other such questions are. Everything in this section is conditional on one recorded fact: it ships only if §31 Phase 0 records upstream sending a stable numeric family of these headers, and not otherwise. On that record, two nullable integer columns are added to the terminal attempt row and filled by parsing the exact header names Phase 0 recorded, never a body, leaving them null whenever a header is absent or does not parse. Nothing routes, throttles, retries, or waits on them; their only reader is the weekly re-derivation of §30.10, which today reads silence as evidence of nothing. If Phase 0 finds no such headers, nothing ships and the schema gains nothing, which is the same conditional the observation decoder above already carries.

Phase 5 owns both halves of that, and neither belongs to Phase 2. The migration that adds the columns is a Phase 5 deliverable ordered before the projection that fills them, rather than a line in the initial migration Phase 2 delivers, so that a Phase 0 answer of no leaves the schema exactly as Phase 2 shipped it and a later yes is an ordinary forward migration. Phase 5’s gate is the three-place rule §0 states for any column: either the two columns are declared in §15.5, constrained in §15.8, and read by a recipe in §30.3 that §28.13 executes, or Phase 0 recorded no such header family and none of those three places changed.

The columns are not declared in §15.5 ahead of that answer. A conditional behavior is cheap to describe and a conditional column is not, because the schema is the thing §15.10 forbids updating and a migration is the sanctioned way to add to it; the condition is recorded there instead, in the form that section already uses for a column it has declined.

## 15. Durable data model

### 15.1 Store choice

Use SQLite through the cgo-free `modernc.org/sqlite` driver.

The decisive property is that the proxy reads its own log. Startup recovers session affinity from recent history, so the store must answer a predicate and an ordering over a table that grows for months, before the listener binds, not merely accept an append.

Rationale:

- Recovery queries over recent history, which is what affinity restoration is.
- Typed nullable fields.
- Transactional append.
- Unique IDs and constraints.
- Efficient account/session/time queries.
- Safe concurrent reads by local analysis tools.
- Crash-resistant commits.
- Startup affinity recovery.
- Forward-only schema migrations.

The accepted costs are a larger binary and one pinned third-party dependency.

### 15.2 SQLite configuration

- Open exactly `LLMUX_DB_PATH`.
- Require an absolute path.
- Require the parent directory to exist, to be owned by the service user, and to deny write access to group and others. Checking the file is not enough on its own: `Lstat` followed by open is a race, and a directory nobody else can write to is what makes the symlink and mode checks below mean something rather than merely narrow a window.
- Do not silently fall back to another path or memory.
- If the database is absent, pre-create it atomically with `O_CREATE|O_EXCL` and mode `0600`, close it, then let the SQLite driver open it.
- Reject an existing symbolic link using `Lstat`.
- Reject an existing path that is not a regular file.
- Reject group/other-readable existing files.
- Recheck database and SQLite sidecar permissions after enabling WAL.
- Use WAL journal mode.
- Set `PRAGMA foreign_keys` on and verify it by reading it back, on every connection. SQLite defaults it off and scopes it per connection, so a schema declaring that a dispatch row references an existing admission enforces nothing until each connection turns it on. Declared and unenforced is worse than absent: §15.8 would describe a guarantee the store does not provide, and an orphan row would surface in a query months later instead of failing the insert that wrote it.
- Set `PRAGMA trusted_schema` off, so no object in the schema can cause a function to run during an ordinary statement.
- Run no whole-database integrity scan at startup. `PRAGMA quick_check` and `PRAGMA foreign_key_check` cost time proportional to a store this document forbids ever truncating, so on the startup path they buy a check that gets slower every week and delay the listener by an amount nobody chose. They belong to `llmux db check`, which an operator runs deliberately.
- Use full synchronous durability. The alternative, and why the measurement §28.18 owes is not what decides it, are stated below.
- Set `wal_autocheckpoint` to zero and drive every checkpoint from the application. SQLite’s automatic checkpoint fires inside whichever commit crosses its page threshold, and the application does not get to choose which commit that is, so leaving it on would defeat the rule below on exactly the commits it exists to protect.
- Attempt a passive checkpoint after each 256 terminal commits, and again when a terminal commit finds the WAL past its warning threshold. The attempt runs on the maintenance connection, in the foreground of the terminal path that triggered it, once that commit has returned.
- A checkpoint never runs on the writer connection. The admission commit sits between reservation and `Do` on the dispatch critical path, so migrating a WAL there arrives as tens of milliseconds of first-token latency for whichever request drew the short straw. Placing the checkpoint behind a terminal commit is not sufficient on its own: with a single connection the next admission commit queues behind that checkpoint regardless of which commit it followed, and the rule protects nothing it names. The second connection is what makes the separation real, and it is also why the interval is a stated number rather than "a bounded number" of commits.
- Attempt one last passive checkpoint at shutdown, after the stop row and before the store closes.
- Never block request handling on a restart or truncate checkpoint.
- Warn when the WAL keeps growing across those attempts, because that is what checkpoint starvation looks like from inside the process.
- Use a five-second busy timeout, and bound every store operation with a six-second context so SQLite’s own busy handling always finishes before the application deadline fires. An application deadline shorter than the busy timeout would cancel exactly the contended writes the busy timeout exists to let through, and an unbounded one would leave the pending-reservation window of §17.1 with no stated ceiling at all, while a reservation holds one RPM slot and one in-flight slot for the whole time its commit runs.
- Cap the writer at one open and one idle connection, so every durable write serializes through it, and keep the maintenance connection separate from that pool.
- Use parameterized SQL only.
- Check every database error.

`synchronous=NORMAL` is the alternative and it is not taken. Under WAL it loses nothing to a process crash, because a committed WAL write is visible to a restarted process whether or not it was ever synced, so every crash-boundary property the tests of §28.13 assert survives it untouched. What it surrenders is durability across an operating-system crash or a power loss, and with automatic checkpointing switched off that exposure is not the newest commit but everything the operating system has not yet written back, a quantity this process neither controls nor can observe.

What that would cost is the one thing the admission row is for. §2 states without qualification that a dispatch which left this process has evidence that it started, §15.11 and §12 step 18 refuse to serve rather than dispatch unrecorded, and §21.7 reconstructs an ambiguous send from exactly these rows. Under NORMAL that promise acquires a power-loss exception, and the failure it admits is undetectable from inside: an admission write that fails is something the process knows about and answers with a 503, while an admission lost to a power cut leaves nothing to notice and nothing to reconstruct from. Those are different trades and they deserve different answers. Refusing a detected failure costs nothing while the store is healthy; narrowing an undetectable one costs one fsync per dispatch, forever.

That fsync is what full durability costs, on a path whose other component is an upstream call measured in seconds, plus whatever queueing it creates against every other statement on the writer connection. §28.18 measures it, and the number is worth having for its own sake, since it sizes the pending-reservation window of §17.1 and says what the one durable write on the dispatch critical path actually charges. It is not what decides this bullet. The rolling ceiling stopped riding on this write when §16.3 moved to the monotonic clock, and what still rides on it is evidence, which a measurement of latency cannot price. The first-event metric is an argument in neither direction, because §14.1 anchors it at the `Do` invocation and the admission commit therefore sits outside the number it would otherwise have polluted.

SQLite-managed `-wal` and `-shm` files are part of the one embedded store, not separate services or application logs.

A checkpoint cannot reset the WAL while a reader still holds an older snapshot, and this design explicitly invites a local analysis tool to read the store while the proxy serves. A long-running query therefore does not merely slow a checkpoint down, it prevents completion for as long as it runs, and every commit in the meantime extends a file that nothing is truncating. The proxy cannot fix that from its side, so it does the two things it can: it keeps trying passively, and it says so when the WAL grows anyway.

If initial migration fails after a new file was created, preserve the file for diagnosis and fail startup. Do not delete or replace an existing store automatically.

### 15.3 Record granularity

Four append-only tables exist, and the split is by commit point rather than by subject.

`dispatch_admission` holds the evidence a dispatch was authorized, and one row is committed synchronously before every possible upstream dispatch:

| Field | Type/nullability | Meaning |
| --- | --- | --- |
| `attempt_id` | Text, primary key | Proxy-generated dispatch identity |
| `logical_request_id` | Text, non-null | Groups one client request’s attempts |
| `attempt_no` | Integer, non-null | 1-based dispatch count within the logical request |
| `account_label` | Text, non-null | `k1`, `k2`, or `k3` |
| `requested_alias` | Text, non-null | Exact client alias |
| `upstream_model` | Text, non-null | Fixed resolved upstream model |
| `reserved_at_us` | Integer, non-null | UTC Unix microseconds at reservation |
| `limiter_rpm_used` | Integer, non-null | Post-reservation snapshot |
| `limiter_in_flight` | Integer, non-null | Post-reservation snapshot |

An admission row carries only what identifies the attempt it authorized and the limiter state that authorized it. It is never updated, so an admission without a matching terminal attempt row is itself a fact: the process crashed, stopped between the commit and the send, or lost its store before the attempt finished. Which of the three it was is not recoverable and does not need to be, because nothing the next process decides is read out of these rows.

`reserved_at_us` records the reservation and not the dispatch, because the row is committed before the dispatch it authorizes exists to be timed. The live window is measured at the dispatch boundary all the same, so the two differ by the commit's own duration, bounded by the store-operation ceiling, which is why no reader of this column may treat it as the instant a request left. The column that does hold that instant is `attempt_log.event_at_us` on the terminal row, which is written afterwards and can therefore carry it.

`attempt_log` holds what became known after the fact.

Record kinds:

- `dispatch`: one actual call to the upstream HTTP client, referencing its required `attempt_id`.
- `selection_skip`: one distinct local candidate rejection due to rate or account health.
- `selection_failure`: one account-acquisition phase that ended without any dispatch.

There is still no separate logical-request summary table. Client-visible outcome, final token counts, retry amplification, and end-to-end latency are all reconstructable from the attempt rows of one `logical_request_id`: the highest `sequence_no` row is what the client saw, its token counts are the final response’s, and counting `dispatch` rows is the amplification. A summary table would store no fact that is not already there, and would introduce the one failure the derived query cannot have, which is the two copies disagreeing. §30.3 therefore owes a tested recipe for each of those questions instead. The rule is the one applied to the omitted columns in §15.5: a table is cheaper to add in a later migration than two writers of one fact are to keep in agreement. The lifecycle table below is not that case, and what separates them is the test rather than the subject, which is why the argument is written out where it is defined.

A logical request may therefore contain:

- Several selection-skip rows.
- Several dispatched retry rows.
- One final dispatched row.
- Or, if no account can be acquired, several selection-skip rows followed by one selection-failure row.

Pre-routing local failures do not produce attempt rows.

Within one account-selection phase, repeated observations of the same `(account, reason)` pair are aggregated into one skip record with an observation count. A changed reason is a new skip fact. This prevents wake/recheck loops from amplifying the log while preserving every distinct reason an account was passed over.

`process_event` holds one row per lifecycle edge of the proxy process itself:

| Field | Type/nullability | Meaning |
| --- | --- | --- |
| `record_id` | Text, primary key | Proxy-generated row ID |
| `process_instance_id` | Text, non-null | Proxy-generated identity shared by one process’s start and stop rows |
| `event_kind` | Text, non-null | `process_start` or `process_stop` |
| `at_us` | Integer, non-null | UTC Unix microseconds |
| `process_elapsed_us` | Integer, nullable | Monotonic duration from the start edge to the stop edge; null on `process_start` |
| `version` | Text, non-null | Binary version, derived from build info the way §30.1 derives it |
| `revision` | Text, non-null | VCS revision from build info |
| `schema_version` | Integer, non-null | `PRAGMA user_version` after migration |

A start row is appended once migration and recovery have succeeded and before readiness is announced, and failing to append it is a fatal startup like every other write failure at that point. A stop row is appended by every shutdown the process survives to perform, orderly or forced, after handlers drain and before the store closes; failing to append that one is a sanitized stderr event and does not change the exit status, because by then there is nothing left to protect. The absence of a stop row therefore means the process never reached its shutdown path, or reached it and could not write there, which is the failure stated one sentence above and tabulated in §24.5. It is the rarer one and it is the only other one, so an unmatched start row is an upper bound on unclean deaths rather than a count of them. Whether a shutdown was orderly or forced is deliberately not encoded here, because the process log already carries it and a stored vocabulary with no reader is exactly what §15.5 refuses.

The two rows of one run are joined by `process_instance_id` and not by time, because `at_us` is wall time and pairing on it fails in exactly the case the pairing exists to report. A process that starts and dies, followed by a backward correction and a second process that starts and stops, leaves four rows whose wall order puts the second run’s stop before the first run’s start, and an operator asking which run ended uncleanly is told the wrong one. Subtracting the two stamps has the same defect one step earlier: a backward step inside a ten-minute run reports a negative uptime, and §24.6 guarantees only that a backward change cannot make a monotonic duration negative, which this span was not. So the span is measured on the monotonic clock like every other duration here and written on the stop row, and the wall stamps keep the job they are good at, which is saying when an event happened rather than how long anything took. An unclean stop becomes a start row that no stop row shares an identity with, which is a question the schema answers instead of a shape someone reads out of an ordering.

This table passes the test the summary table failed, and the difference is worth stating because the two look alike from a distance. Every field here is a fact no attempt row contains. An idle proxy and a stopped proxy produce identical row sets, so uptime is not derivable from the attempt log at all, and nothing in that log records which binary wrote a given span of rows or which schema version was in force at the time. There is one writer, the lifecycle path, so there is no second copy of a fact to drift from. Stderr already carries the same information, but stderr is ephemeral and cannot be queried alongside the rows, which is the whole reason this store holds its own history. The cost is two inserts per process lifetime, and what it buys is the difference between a missing `eod` row meaning the consumer failed and it meaning the proxy was not running.

`unrouted_request` holds one row per authenticated chat request that received a local response before any account-selection phase began:

| Field | Type/nullability | Meaning |
| --- | --- | --- |
| `record_id` | Text, primary key | Proxy-generated row ID |
| `logical_request_id` | Text, unique, non-null | The value the client received as `X-LLMux-Request-ID` |
| `started_at_us` | Integer, non-null | UTC Unix microseconds at handler start |
| `finished_at_us` | Integer, non-null | UTC Unix microseconds at the local response |
| `session_key` | Text, nullable | Versioned keyed digest, present only when the header was read and validated before the rejection |
| `downstream_status` | Integer, non-null | Status written to the client |
| `local_error_code` | Text, non-null | The `error.code` this request answered with, from the fixed vocabulary of §22 |

Membership is exactly the set of requests that reach no account-selection phase: a malformed or oversized body, a compressed body, a non-empty query string, an oversized session header, an unknown alias, a rejected nesting depth, a handler or memory overload, and a panic recovered before routing. Everything past that point already writes to `attempt_log`, including a phase that acquires no account at all, a cancellation during a selection wait, and an admission-store failure, whose phase ends without dispatch and appends its skips and a terminal selection failure like any other.

This is not the logical-request summary table declined above, and the test that separates them is the one that table failed and the lifecycle table passed. A summary was refused because every fact in it already exists in the terminal attempt row of its `logical_request_id`, so it would hold a second copy of a derivable fact and give two writers a way to disagree. These rows are defined by the absence of that terminal row. A request appears here only when it appears nowhere else, so across the two tables there is exactly one writer per logical request, and no fact exists in both. What it closes is a hole this document opened itself: it promises every authenticated chat response an `X-LLMux-Request-ID`, and §32 claims a client can find its own request in SQLite with that identifier, which for a malformed body, an unknown alias, or a memory overload was not true, because those requests wrote nothing anywhere.

Its two stamps say when the request arrived and when it was answered, and they are not a latency. Subtracting them is the thing §24.6 forbids everywhere, and this row is where it looks harmless because the interval is short; a wall step landing inside it produces a negative number just the same. No recipe in §30.3 asks these rows how long anything took, and if one ever does the answer is a monotonic duration column rather than an arithmetic on the two stamps that exist.

The row stops at the outcome and holds no client string the proxy did not validate. The requested alias is deliberately absent: by definition it either failed to resolve or was never read, and persisting it would let an authenticated caller turn an arbitrary `model` value into durable data, which is the same objection §6.6 raises against storing raw session identifiers. A request whose client vanished before any status was written appears in neither table, which is a decision rather than an omission: the row exists so that an identifier a client is holding can be resolved, and a client that never received one is holding nothing.

### 15.4 IDs

- Each admitted chat request gets a proxy-generated `logical_request_id`.
- Each dispatch gets a proxy-generated `attempt_id`, created with its admission row and carried onto the terminal attempt row that reports it.
- Every persisted row gets a proxy-generated `record_id`.
- IDs are 128 random bits from `crypto/rand`.
- They are encoded as 32 lowercase hexadecimal characters.
- Failure of the secure random source rejects the request before account dispatch.
- SQLite uniqueness remains the final collision guard.
- No upstream-generated ID is read into either field.

### 15.5 `attempt_log` fields

| Field | Type/nullability | Meaning |
| --- | --- | --- |
| `record_id` | Text, primary key | Proxy-generated row ID |
| `logical_request_id` | Text, non-null | Groups one client request’s rows |
| `attempt_id` | Text, nullable | Required for dispatch rows; references `dispatch_admission`. Null for skips and selection failures |
| `sequence_no` | Integer, non-null | Foreground event order within the logical request |
| `selection_no` | Integer, non-null | 1-based account-acquisition phase; normally the prospective attempt number |
| `record_kind` | Text, non-null | `dispatch`, `selection_skip`, or `selection_failure` |
| `requested_alias` | Text, non-null | Exact client alias |
| `base_alias` | Text, non-null | Resolved base alias |
| `upstream_model` | Text, non-null | Fixed resolved upstream model |
| `session_key` | Text, nullable | Versioned keyed digest of the non-empty session header |
| `pin_account_at_start` | Text, nullable | Session pin before routing |
| `account_label` | Text, nullable | `k1`, `k2`, or `k3`; null only for terminal selection failure |
| `attempt_no` | Integer, nullable | 1-based dispatch count; null for skips |
| `is_spill` | Boolean integer, non-null | Dispatch differs from valid initial pin |
| `spill_from_account` | Text, nullable | Original pin for a spill |
| `event_at_us` | Integer, non-null | UTC Unix microseconds at the boundary this row records: the `Do` invocation for a dispatch, the observation for a skip, the terminal decision for a selection failure |
| `finished_at_us` | Integer, non-null | UTC Unix microseconds at terminal record |
| `selection_wait_us` | Integer, nullable | Phase start through lease acquisition/failure; null for individual skips |
| `attempt_duration_us` | Integer, nullable | Monotonic duration of the upstream call, `Do` through response close |
| `logical_elapsed_us` | Integer, non-null | Handler start through this row |
| `time_to_first_event_us` | Integer, nullable | Time to the first complete non-empty SSE data event |
| `outcome` | Text, non-null | Stable terminal outcome |
| `upstream_status_code` | Integer, nullable | Upstream HTTP status |
| `error_class` | Text, nullable | Stable low-cardinality classifier |
| `retry_disposition` | Text, non-null | Retry/finality decision |
| `retry_delay_ms` | Integer, nullable | Selected next delay |
| `retry_after_s` | Integer, nullable | Local capacity response’s advertised retry delay |
| `upstream_retry_after_s` | Integer, nullable | Upstream’s advertised retry delay in whole seconds, unclamped |
| `response_committed` | Boolean integer, non-null | Downstream response had begun |
| `request_streaming` | Boolean integer, nullable | Raw top-level stream was exactly true |
| `prompt_tokens` | Integer, nullable | Upstream-reported count |
| `completion_tokens` | Integer, nullable | Upstream-reported count |
| `total_tokens` | Integer, nullable | Upstream-reported count |
| `usage_observation` | Text, nullable | Why the token columns hold what they hold: `not_applicable`, `absent`, `complete`, `malformed`, `truncated`, `unsupported_encoding`, or `limit_exceeded`; null for skips and selection failures |
| `limiter_rpm_used` | Integer, nullable | Per-account snapshot at a skip; null on dispatch rows, which carry it on their admission row |
| `limiter_in_flight` | Integer, nullable | Per-account snapshot at a skip; null on dispatch rows, which carry it on their admission row |
| `skip_reason` | Text, nullable | Local selection reason |
| `skip_observation_count` | Integer, nullable | Repeated identical observations aggregated into a skip row |
| `dropped_header_count` | Integer, nullable | Request headers removed by the allowlist; null for skips |

`event_at_us` is named for the row rather than for a phase because the three record kinds have no common start, and it is anchored at `Do` on a dispatch row for the reason §14.1 gives about the metric next to it. A column called `started_at_us` and filled at reservation is the mislabel this document has now corrected twice: the name says one boundary, the value is taken at another, and everything downstream inherits the displacement. Here that displacement is the admission commit, up to the store-operation ceiling, so a dispatch reserved at 12:00:59 and sent at 12:01:05 would be counted in the wrong minute by the two recipes §30.3 owes and by the ceiling re-derivation those recipes feed, and the error would grow with whatever the filesystem was doing. Renaming a column costs nothing while no code exists, and §15.10 forbids the schema from updating itself afterwards.

The schema contains no body, message, completion, raw session identifier, header value, credential, raw upstream error, upstream ID, cost, price, or currency column.

The schema carries no field that exists only to explain the proxy to itself. A pin move is reconstructed from `session_key`, `account_label`, `is_spill`, and `spill_from_account` ordered by time, and the reopening estimate that drove a wait is reconstructed from the skip rows and the rolling window. Both were considered as stored columns and rejected: neither has a reader today, and a column is cheaper to add in a later migration than a vocabulary is to keep honest without one.

`usage_observation` is the exception that proves the rule above rather than a breach of it, and it earns its place on the test `upstream_retry_after_s` passed: a named reader on the day it is written. A null token count means any of upstream sending no usage object, a streaming client never asking for one, a response encoding the observer could not decode, a line past the observer’s cap, or a truncated stream, and without this column nothing would separate them: §30.3 would be left telling the operator to notice a run of nulls and go and work out which, and it names the column instead. §30.10 re-derives the per-account ceilings from a signal that can quietly fall to zero for one of those reasons without anything reporting it. Seven closed values with a single writer replace a heuristic that whoever reads the runbook next has to re-derive.

A validated consumer label, supplied by the caller in a fixed header, was considered on the same rule and rejected by it. It would be evidence rather than tenancy, since nothing would authenticate, route, or throttle on it, and a closed lowercase vocabulary rejected on mismatch would not reopen the question §6.6 settles for session identifiers. What it lacks is a reader the existing columns cannot serve. With three consumers, one of which is the only sessionless caller, `session_key`, `requested_alias`, and `request_streaming` already separate them, and the header would have to be added to all three callers as one more cutover precondition to buy that. It earns its cost at a fourth consumer, or at a second sessionless one, which is the point at which the attribution in §30.3 stops working; adding the column then is a migration, and a migration is the direction this schema treats as cheap.

Request and response body-size columns were considered on the same rule and fail it in the ordinary direction. They would be content-free metadata of the same class as a token count, and nothing in this store can say today how often a real response passes the 8 MiB precommit bound or how close real traffic comes to the 64 MiB envelope and the aggregate budget. What they lack is a reader. §9.2 fixes those three constants and states that they are not a tuning surface, and the one re-derivation this document does invite is of the per-account ceilings, which reads none of them; a column added now would arrive with a recipe written to justify it. The condition that buys them is a precommit bound or a memory budget that starts binding in production, which the existing overload rejections and their error codes are what makes visible first, and adding the columns at that point is a migration.

The rate-limit projection of §14.4 is recorded here on the same terms, read forward rather than backward. Its reader exists today in §30.10 and the fact it would hold is one no other column can produce, since nothing else in this store can bound the local ceiling from below. What is not established is that upstream sends the headers at all, and that is what the Phase 0 gate answers. So the condition is written down and the columns are not: if the gate finds a stable numeric header family, Phase 5 adds `upstream_rl_limit` and `upstream_rl_remaining` as nullable integers in a migration of its own before filling them, and if it does not, nothing was carried for a fact that never arrives.

### 15.6 Outcome vocabulary

- `succeeded`
- `upstream_http_error`
- `transport_error`
- `deadline_exceeded`
- `client_canceled`
- `response_read_error`
- `response_write_error`
- `selection_skipped`
- `capacity_timeout`
- `no_account_available`
- `internal_error`

### 15.7 Error-class vocabulary

- `rate_limited`
- `upstream_authentication`
- `upstream_client_error`
- `upstream_server_error`
- `invalid_upstream_response`
- `transport_timeout`
- `transport_transient`
- `transport_permanent`
- `client_disconnect`
- `response_truncated`
- `local_deadline`
- `account_disabled`
- `account_cooldown`
- `local_capacity`

`invalid_upstream_response` is the class for a dispatched attempt that ended in an upstream 3xx or 101. Such an attempt is neither a client error nor a server error, and filing it under a transport class would erase the one distinction that makes it findable later. There is deliberately no class for a locally malformed request: a pre-routing local failure produces no attempt row at all, and an upstream 400 is an upstream client error, so nothing could ever write one. A value nothing can write drifts from the code without anything failing, which is the same reason this schema declines a vocabulary with no reader.

Raw Go error strings are never stored.

### 15.7.1 Selection and retry vocabularies

`skip_reason`, on selection-skip rows only:

- `rpm_limit`
- `in_flight_limit`
- `rate_gated`
- `disabled`
- `start_blackout`

`rate_gated` covers both a single 429 and the cooldown circuit, because §20.2 keeps them on one deadline and selection acts on neither differently. `start_blackout` is the one process-wide value: it names every account for the first rolling window of a process's life, which is what §16.3 uses in place of reading rate state back out of the store.

`retry_disposition`, on every row:

- `final`
- `retry_same_account`
- `retry_other_account`
- `retry_named_account`
- `suppressed_class_budget`
- `suppressed_global_budget`
- `suppressed_deadline`
- `not_applicable`

The three suppressed values separate a failure upstream declared final from one this proxy would have retried and could not, which is the difference §30.10 looks for when it asks whether the deadline or the dispatch budget is the binding constraint. `not_applicable` belongs to selection-skip rows, which record no attempt and therefore no retry decision. Both lists are stated because §15.8 requires fixed enums and these two were the ones it never wrote down, and an enum nobody enumerated is a free-text column that the code fills consistently right up until it does not.

### 15.8 Constraints

The schema enforces:

- Unique `record_id`.
- Unique `(logical_request_id, sequence_no)`.
- Unique `attempt_id` and unique `(logical_request_id, attempt_no)` in `dispatch_admission`.
- `attempt_id` required on dispatch rows, null on skip and selection-failure rows, and always referencing an existing admission.
- Limiter snapshots present only on skip rows.
- Fixed record kinds and enums.
- Foreign-key enforcement enabled and verified at runtime on every connection, not merely declared in the schema text.
- Positive `selection_no` for every row.
- Account labels restricted to three values when present.
- Account label required for dispatch and skip rows, and null for selection-failure rows.
- `attempt_no` required only for dispatch records.
- `skip_reason` required only for skip records, restricted to its fixed vocabulary.
- `retry_disposition` restricted to its fixed vocabulary, and `not_applicable` only on skip records.
- `skip_observation_count >= 1` only for skip records.
- `dropped_header_count` non-negative, and present only for dispatch records.
- `usage_observation` present only on dispatch records, restricted to its fixed vocabulary.
- `selection_wait_us` required for dispatch and selection-failure rows.
- `retry_after_s` allowed only for capacity failures.
- `upstream_retry_after_s` non-negative, and allowed only on dispatch rows whose upstream status is 429 or 5xx, which are the two statuses this proxy reads the header on. Whether the header was present is not separately constrained, because a null column says only that upstream stated no delay this proxy could use: the header was absent, or unparseable, or an HTTP-date the response gave no usable `Date` to derive against, which §20.2 stores as nothing, and no column records which of the three it was.
- Non-negative durations.
- Non-negative token counts.
- Spill source required when `is_spill` is true.
- Boolean values restricted to zero/one.
- At most one `process_start` and one `process_stop` for each `process_instance_id`, with `process_elapsed_us` present on the stop row only and non-negative.
- Unique `logical_request_id` in `unrouted_request`, with `local_error_code` restricted to the fixed vocabulary of §22.
- A `logical_request_id` appears in `unrouted_request` or in `attempt_log` and never in both. That one is an application rule asserted by tests rather than a trigger, because enforcing it in the schema means a cross-table query on every insert to buy a guarantee that a single writer per request already provides.

### 15.9 Indexes

Create indexes for:

- `dispatch_admission(account_label, reserved_at_us)`, which is the offline admission-pressure query of §30.3.
- `attempt_log(attempt_id)`, which is the other half of that same §30.3 bullet. The admissions-no-terminal-row figure joins this column against the primary key `dispatch_admission` already carries on `attempt_id`, and the child side is a nullable column with no index of its own, so the join degrades to a scan of the table this document expects to grow for months while the sibling figure beside it in the same bullet is indexed. It is declared here rather than left to a later migration because §15.10 forbids updating the schema in place, which makes an index that ships with the initial migration free and the same index discovered afterwards a migration of its own.
- `attempt_log(event_at_us)`.
- `attempt_log(account_label, event_at_us)`.
- `(logical_request_id, sequence_no)`.
- A partial successful-completion index on `(session_key, finished_at_us DESC)`, which is the session recovery query. It restricts the query to successful completions inside the hour; the arrival order that query then ranks by is computed from the retrieved rows, because it is derived from two columns rather than stored in one.
- `(requested_alias, event_at_us)`.
- `(outcome, event_at_us)`.
- `(error_class, event_at_us)`.
- `unrouted_request(finished_at_us)`.

### 15.10 Append-only enforcement

- The application exposes no update or delete method.
- SQLite triggers reject `UPDATE` and `DELETE` on all four durable tables.
- Schema metadata uses `PRAGMA user_version`.
- Migrations are numbered, embedded, forward-only, and transactional.
- A database newer than the binary understands causes fatal startup.
- No automatic retention, compaction, deletion, or vacuum exists.

### 15.11 Commit timing and crash behavior

Correctness evidence and analytics have different commit points, because they answer to different failures.

1. `dispatch_admission` commits synchronously after the in-memory reservation and before `http.Client.Do`.
2. Each selection phase accumulates a bounded set of skip facts in memory. When that phase’s dispatch becomes terminal, or the phase itself ends without dispatch, the skip rows and the terminal dispatch or failure row are inserted in one SQLite transaction.
3. `process_event` commits once at startup and once at shutdown. It is the only durable write that belongs to no request, which is why its failure modes are read against the lifecycle rather than against a client result.
4. `unrouted_request` commits once, in its own transaction, and only for a request that never reached account selection. It is appended after the local response has been written, so a slow or failing store can neither delay nor alter a local rejection, and it uses the same bounded store context as the others so that a client cancellation or an expired deadline cannot suppress the record of exactly that.

This means:

- Every actual network dispatch has durable pre-dispatch evidence, so the log records what the process was authorized to send and not only what it managed to finish.
- That implication runs one way. An admission with no terminal row does not say whether `Do` was ever reached, because the crash may have fallen on either side of it, and §15.3 names the three things it can mean and resolves it no further. It is an upper bound on what left, never a count of it.
- One dispatched attempt has one complete immutable terminal row.
- A terminal capacity failure is explicit rather than inferable from the last skip.
- A phase normally incurs one terminal commit rather than one commit per account recheck, on top of one admission commit per dispatch.
- Status, token counts, retry decision, and durations coexist in that terminal row.
- A process crash during an active attempt can lose that attempt’s result metadata and its pending skip rows, but not the record that it started.
- A separate start row followed by an update is deliberately not used; the admission row is a distinct immutable fact, not a mutable draft of the terminal one.
- A streaming success may reach the client before its final log insert fails.
- A logging failure cannot retroactively replace an upstream success.
- An admission failure produces a sanitized stderr event, prevents the dispatch, and returns local 503. It is the one persistence failure that stops work, because it is the one that would otherwise create traffic no restart could account for.
- Terminal log failures produce sanitized stderr events and do not stop serving, including for traffic already admitted.
- Database failure at startup is fatal.

Admission and terminal writes both use bounded store contexts derived from the application force-shutdown context. Client cancellation and logical-deadline expiry must not suppress the record of that cancellation or expiry.

The synchronous transaction is attempted:

- Before beginning a retry, so earlier attempt evidence is not intentionally deferred behind later network work.
- Before returning a local capacity error.
- After final response relay, because the final usage and duration are not known earlier.

The bounded phase batch contains at most one aggregate row per account/reason combination plus one terminal row. There is no unbounded logging queue and no background log-writer goroutine.

## 16. Account coordinator

### 16.1 Account state

There are exactly three account records.

Each contains:

- Label.
- Reference to its key.
- Ordered deque of recent dispatch-start timestamps.
- Current in-flight count.
- Health state.
- Rate gate deadline, advanced by any 429 and floored by the cooldown circuit.
- Recent 429 timestamps.
- Notification generation.

Health states:

- `enabled`
- `cooling_down`
- `disabled`

### 16.2 Session state

Each session key maps to:

- Pinned account.
- Expiry.
- Request-arrival sequence that last established the pin.
- Provisional or confirmed state.
- Provisional generation and the count of requests currently holding it.

Rules:

- The first dispatch for a new session atomically creates a provisional pin.
- Concurrent first requests for that session see the same pin and attach as holders of the same provisional generation.
- A fully successful response confirms and refreshes it.
- A terminally failed request releases its holder.
- When the last holder of an unconfirmed generation fails, the pin is removed immediately rather than left to expire.
- A provisional pin’s safety expiry is the logical request deadline, not one hour. Nothing that has never succeeded earns an hour of affinity.
- TTL is one hour of wall-clock time from successful completion.
- A successful spill changes the pin.
- A failed or partial spill does not.
- A successful explicit `-kN` request changes the pin to that account.
- A sequence guard prevents an older concurrent request from overwriting a newer pin update.
- An expired pin is removed lazily.
- Every 256 session operations, a foreground sweep removes expired entries.
- The map holds at most 4096 pins. When it is full, a session that has none routes as an unpinned base alias and creates no entry, and a warning is emitted.
- There is no cleanup goroutine.

The affinity hour is stated on the wall clock rather than left to the implementation, because §16.3 rebuilds a pin out of `finished_at_us` and that column is wall time. A live expiry measured monotonically would have to be reconstructed at startup from a quantity that cannot produce it: a clock corrected backward between the completion and the restart makes the row look younger than it is, and the recovered pin then outlives its hour by the size of the step, while a forward correction discards a pin that is still inside it. On one clock there is no conversion left to get wrong, and what a clock step costs is only what a wall step always costs here, which is a conversation keeping or losing its account earlier or later than an hour. Nothing else rides on this deadline. The rolling ceiling and the post-start blackout are monotonic by §17.1 and §16.3 precisely because a wrong answer there is an over-admission, while a pin is a cache-locality preference whose worst outcome is starting the next turn on the other of two accounts. The provisional pin is the exception that needs no rule of its own: its expiry is the request’s own context deadline, it is never persisted, and no restart recovers one.

The map needs a ceiling because a pin outlives the request that created it and is keyed on client-chosen text. Every other allocation in this design is charged to a request and released when it ends, which is what §4 invariant 16 bounds; a session pin is the one thing that survives its request, so a consumer that generated a fresh identifier per turn, by intent or by defect, would grow the map for an hour at a time with nothing to stop it. At the ceiling the proxy refuses rather than evicting: refusing costs a new session its affinity, while evicting costs an established conversation the prefix cache the pin exists to preserve, and the established one is the one with something to lose. The sweep clears the expired entries, so the ceiling releases itself.

The sweep stays a full pass rather than becoming an indexed expiry heap. A heap would make expiry logarithmic and refresh a fix-up instead of a rescan, and it would be a second structure that has to agree with the map through pin creation, provisional confirmation, TTL refresh, holder release, and lazy removal, which is five places for the two to disagree. Against a map the rule above bounds at 4096 entries, holding live conversations that number in the tens for three local consumers, the pass costs less than the lock acquisition around it. The ceiling is what makes that true, and it is the thing to revisit if it stops being true.

### 16.3 Startup recovery

Before listening:

- Recover, for every `session_key` with a successful completion in the previous wall-clock hour, the account served by whichever of those requests arrived last.
- Preserve the original completion-based expiry, which §16.2 measures on the same wall clock this query reads, so nothing is converted between clocks here.
- Recover no rate state. The post-start dispatch blackout holds every account closed for the first 60 monotonic seconds of process life, which is one complete rolling window.
- Set in-flight counts to zero.
- Do not restore disabled state, because restart is how corrected credentials are installed.
- Do not call upstream to validate recovered state.

The blackout is measured on the monotonic clock of the new process, and that is what makes it immune to the failure a recovery query cannot escape. Recovered instants are UTC wall time while the live window is monotonic, so a wall clock corrected forward across the restart, which is the ordinary step at boot and therefore exactly when recovery would run, makes recovered admissions look older than they are, expires them early, and readmits capacity the previous process had already spent. §24.6 clamps a recovered timestamp that lands in the future, and an error in the other direction has no clamp available at all, because nothing in a row says it is older than it looks. Sixty monotonic seconds of refusal need no timestamps: every dispatch of the previous process precedes that process's death, every dispatch of this one follows this one's start by a full window, and no 60-second interval can contain both. What the proof rests on is that the two processes did not overlap, which is the single-instance property §17.5 makes a checked condition of startup.

The blackout is unconditional, and the tempting exception is the one that would break it. A store holding no admission rows looks like a fresh install with nothing to be conservative about, but it is also what §30.4 hands a restarted process after an archive rotation, whose real dispatches are minutes old in a database that was moved aside. Skipping the wait on an empty store would reopen exactly the hole that procedure used to close by waiting.

The cost is up to one quiet minute after every start. Selection treats the blackout as a deterministic blocker with a known reopening instant, so a request arriving during it waits inside the ordinary 60-second acquisition ceiling and then proceeds, and only a request arriving in the first instants of process life can exhaust that ceiling and receive the local 429, with an accurate `Retry-After`, that §30.7 already requires every caller to handle. Restarts are rare by design, since restart is this proxy's only reload mechanism, and the minute is the honest price of a guarantee that needs no clock it cannot trust.

Pin recovery orders by arrival and not by completion, because the live coordinator does. §16.2 guards a pin update with the request-arrival sequence, so when a newer request finishes first and an older one finishes afterwards, the live pin belongs to the newer request and the older completion is refused. Recovering the account with the newest `finished_at_us` reverses precisely that case, and a restart inside the hour would then move a live conversation onto an account the running process had already decided against, which is the one thing affinity exists to avoid. Arrival order needs no column of its own: `logical_elapsed_us` is measured from handler start, so `finished_at_us` less `logical_elapsed_us` is the arrival instant of the request that wrote the row, and both columns are non-null everywhere. What the derivation is not is proof against the wall clock. `finished_at_us` is wall time, so a step landing between two finishes moves the two derived arrivals against each other exactly as a step between two arrivals would move two stored stamps, and for a long-running request against a short one it moves them further. The derived instant is only as good as the wall clock was at each finish, which is a reason to prefer it for costing nothing rather than for being safer. Ties therefore resolve by a stated rule instead of by map order: equal derived arrivals go to the later `finished_at_us`, and then to the greater `record_id`. The residual exposure is a clock step between two successful completions of one session inside one hour, followed by a restart, and its cost is bounded to starting the next turn on the other of two accounts that both hold a recent prefix.

## 17. Concurrency-correct rate accounting

### 17.1 Exact admission algorithm

All account and session state is guarded by one coordinator mutex.

For an account candidate:

1. Read the monotonic clock.
2. Reject every candidate while the post-start dispatch blackout has not expired, recording `start_blackout`. This is the one place a slot is granted, so it is the one place the blackout has to hold, and putting it here is what stops three selection paths from each needing their own copy of the rule.
3. Remove dispatch timestamps at or before `now - 60 seconds`.
4. Expire a gate deadline that has passed, returning an account the cooldown circuit had marked `cooling_down` to `enabled`.
5. Reject a disabled account, or one whose gate deadline has not passed.
6. Reject if in-flight is already 12.
7. Reject if the remaining dispatch timestamps plus the account’s pending reservations already total 60.
8. Otherwise increment pending reservations.
9. Increment in-flight.
10. Return an immutable release-once pending lease.
11. Unlock.

The rate check and both mutations are one critical section. Concurrent goroutines cannot claim the same final slot.

A second critical section closes the reservation. Once the admission row has committed, the caller reacquires the mutex, decrements the account’s pending count, appends the current monotonic instant to its dispatch deque, unlocks, and calls `http.Client.Do`. Neither section performs I/O of any kind.

The window is defined over the instants of the calls it exists to bound, and the pending count is what makes that safe to measure there. Dating a slot from its reservation instead puts the admission commit inside the number: the slot expires from the window that much sooner than the call it authorized turns a minute old, so a saturated account can put more than 60 real dispatches inside one real minute by exactly the commit latency, and the excess grows with whatever the store is doing at the time. Counting pending reservations against the ceiling preserves the property the single critical section provided, which is that a concurrent caller cannot be admitted into a slot that is about to be taken. This is the correction §14.1 already makes for `time_to_first_event`: a quantity named for a boundary is measured at that boundary, and a durable commit placed in front of it does not get to move it.

### 17.2 Limit semantics

| Event | RPM slot | In-flight slot |
| --- | --- | --- |
| Candidate inspected and skipped | No | No |
| Pending reservation | Occupies a slot; freed only if the admission commit fails | Held immediately |
| `Do` invocation | Timestamp installed, never refunded | Already held |
| Successful `Do` invocation | Already consumed | Until body closes |
| `Do` transport failure | Yes | Until `Do` returns |
| Upstream 4xx/5xx | Yes | Until body closes |
| Retry dispatch | Another slot | Another lease |
| Waiting/backoff | No | No |
| `/v1/models` | No | No |
| Local pre-routing rejection | No | No |

Every finalized reservation is counted because the proxy cannot prove that a request failing at the send boundary was unseen by upstream. All body rewriting, URL construction, and header filtering occur before reservation, so the only fallible step left between reservation and `Do` is the admission commit, whose failure cancels the reservation outright rather than being retried or ignored.

### 17.3 Wait and wake

The coordinator uses a replace-on-notify channel:

- State changes close the current channel and replace it under lock.
- Rejected callers receive that channel, the rejection reason, limiter snapshots, and the account’s earliest known timed eligibility.
- They unlock before waiting.
- They wait on:
  - Request cancellation.
  - Coordinator notification.
  - A reusable timer.
- Eligibility is always rechecked under the mutex.
- Rolling-window expiry requires no refill goroutine.
- Skip observations are accumulated only after the lock is released.
- A single account-selection phase never waits longer than 60 seconds.

### 17.4 Rationale

An exact rolling deque directly enforces “60 in any 60 seconds.”

It avoids:

- Token-bucket bursts beyond 60 starts in a sliding minute.
- Fixed-minute counters permitting 50 adjacent starts across a boundary.
- Per-alias buckets that multiply real-account capacity.

Each account retains at most 60 rate timestamps, so the memory cost is trivial.

### 17.5 Multi-process limitation

Correctness is process-local.

- One process owns the listener and all keys.
- Starting another proxy against the same store would create independent counters, which is why startup takes an exclusive advisory lock beside the database and holds it for the process lifetime.
- Multi-process coordination is outside scope. The lock coordinates nothing; it refuses.
- The operational deployment must run one instance, and the lock makes that a checked condition of startup rather than a sentence in a runbook.

The bind was an accidental mutex and stopped being one the moment a second environment file could carry a different `LLMUX_LISTEN_ADDR`. Two processes on one store double every ceiling and interleave their writes, which is the operator error this document already converts into a fatal check everywhere else it appears: duplicate account keys are fatal precisely because they would create two limiters for one real account, and two processes are that same failure at process granularity. It is also what the blackout of §16.3 rests on. That proof holds because every dispatch of the previous process precedes the start of the next one, and only a single-instance guarantee makes that ordering true. An advisory lock dies with the process holding it, including one killed with no chance to clean up, so there is no stale lock to recover and no state to reconcile. The read-only `db` subcommands never take it, because a live proxy must remain inspectable and backable-up.

## 18. Account selection

### 18.1 Unpinned base alias

1. Generate a random permutation of `k1`, `k2`, `k3`.
2. Lazily prune and expire state.
3. Consider accounts in that order.
4. Record distinct local skip decisions.
5. Atomically acquire the first eligible account.
6. If none is currently eligible but some may recover, wait.
7. Reshuffle after waking.
8. If every account is disabled, return local 503 immediately.
9. Stop waiting after 60 seconds even when the logical request has more time remaining.
10. On temporary capacity exhaustion, append a terminal selection failure and return local 429 with `Retry-After`.

Shuffle is for fairness, not security. Tests inject deterministic permutations.

### 18.2 New session

- Selection follows the unpinned base flow.
- Account acquisition and provisional session pin installation occur atomically.
- Concurrent new-session requests therefore cannot independently choose different initial accounts.
- The provisional pin lives only while a request holds it, and never past the logical request deadline. It becomes an hour-long pin only when a successful completion confirms it.

### 18.3 Existing session

- Try the live pin first.
- If it has capacity, use it without shuffling.
- If disabled, remove the pin, record the skip, and choose a new account immediately.
- If temporarily saturated or cooling, use the bounded stall/spill policy.
- An explicit account alias overrides the session pin.

### 18.4 Explicit `-kN` alias

- Only the named account is eligible.
- It never spills.
- It waits for temporary rate, in-flight, or cooldown capacity for at most 60 seconds.
- It still respects RPM, in-flight, cooldown, logical-deadline, and retry limits.
- If no capacity is acquired, it returns local 429 with `Retry-After`.
- It fails immediately with local 503 if the account is disabled.
- On success, it updates the session pin if a session ID was supplied.

### 18.5 Lease lifecycle

A lease contains:

- Account label.
- Reservation time.
- Limiter snapshots.
- Release-once guard.

Rules:

- Install cleanup immediately after acquisition.
- Release after upstream body closure, which for a response buffered in full precedes the downstream write, so a slow reader never holds an account slot.
- Release on success, errors, panic, retry, cancellation, and shutdown.
- Decrement in-flight exactly once.
- Retain the RPM timestamp until it naturally expires.
- Notify waiters on release.
- Never hold coordinator state across network or database I/O.

A lease is pending between reservation and the commit of its admission row:

- A pending reservation counts against RPM and in-flight for the whole time its admission transaction runs, so a concurrent caller can never be admitted into a slot that is about to be taken.
- On commit success the reservation is finalized under the coordinator mutex, which installs the account’s dispatch timestamp, and `Do` follows immediately.
- On commit failure the lease is canceled, which is the one path that frees a pending slot: no dispatch happened, and no evidence of one exists.
- A crash between commit and dispatch leaves an admission with no dispatch. That is the deliberate direction of the error, because the opposite one is a real dispatch with no record.

Finalization has no failure branch, and the case that argues for one is answered here rather than left open. A concurrent 401 can disable an account while an admission commit is running, so a request can finalize into an account the process has just learned is revoked. Refusing to dispatch there would need a durable record of the refusal, because §15.3 reads an admission with no terminal row as a crash, and that record would need a record kind, an outcome value, and constraints of its own. It would buy nothing. A request inside the commit window is in exactly the position of one already in flight to that account when the 401 arrived, and this document already says what happens to those: it receives its own 401, the account is disabled either way, and a flexible route fails over. What it costs is one slot on an account that will not be used again for the rest of the process lifetime.

## 19. Saturated pinned-session policy

### 19.1 Chosen behavior

Use a reopen-aware bounded stall followed by spill.

1. Set the pin deadline to the earlier of five seconds, the 60-second acquisition deadline, and the logical request deadline.
2. Inspect the pinned account atomically and record its rejection reason and any known reopening time.
3. If the pin is disabled, do not wait; proceed to alternatives immediately.
4. For deterministic blockers:
   - RPM reopening is the oldest retained admission plus 60 seconds.
   - Rate-gate reopening is the account’s gate deadline, whether a single 429 or the cooldown circuit set it.
   - Blackout reopening is one rolling window after this process started, and it blocks every account at once, so no alternative is any closer.
   - If several deterministic blockers apply, use the latest reopening time because all must clear.
5. If the deterministic reopening time is later than the pin deadline, do not burn the five-second grace pointlessly; proceed to alternatives immediately.
6. If deterministic reopening falls within the grace, wait exactly until that time or an earlier coordinator notification, then retry the pin.
7. For in-flight saturation, whose release time is unknowable, wait on notification until the pin deadline.
8. If deterministic and in-flight blockers coexist, a deterministic blocker beyond the grace still makes waiting pointless. Otherwise wait on both notification and the known timer.
9. If the pin becomes eligible during grace, reserve it and preserve the cache.
10. When grace ends or is provably pointless, consider all accounts with the original pin first and the alternatives in a fresh shuffled order.
11. If an alternative wins, mark the dispatch as a spill from the original pin.
12. If every account remains temporarily unavailable, wait for any account only until the 60-second acquisition deadline.
13. If acquisition expires, return local 429 with a computed `Retry-After`.
14. Re-pin only after the spilled response is 2xx and fully relayed.

### 19.2 Rationale

A pure stall could block an interactive request behind three unrelated calls that each last several minutes. Immediate spill would discard useful prefix caching even when a request is about to finish.

Five seconds is a maximum, not a mandatory delay. Known RPM and cooldown timing lets the proxy wait only when preservation can plausibly succeed inside that budget.

After a successful spill, the new account has processed the newest complete conversation prefix. Re-pinning therefore maximizes cache continuity for the next turn.

### 19.3 Consequences

- A spilled turn may reprocess the entire prompt.
- The proxy records the spill but attaches no currency value.
- Failed and partial spills do not move affinity.
- Explicit account aliases never spill.
- An RPM window known to reopen in four seconds waits approximately four seconds; one known to reopen in 45 seconds spills immediately.
- When all accounts are saturated, the proxy waits no more than 60 seconds before giving the client an actionable 429.
- Concurrent requests using one session cannot guarantee conversational ordering, though sequence guards prevent stale pin overwrites.

## 20. Account health

### 20.1 Upstream credential failures

Upstream 401 means `upstream_authentication`.

Actions:

- Mark the account disabled as soon as the status is classified, under the coordinator lock, before the body is drained and before anything is written to SQLite.
- Remove session pins that point to it.
- Wake waiting requests.
- For a flexible route, retry the current logical request on another eligible account, within the global dispatch budget and with no backoff. The failure is deterministic for the account rather than for the request, and routing around a broken account is the one job this proxy exists to do. Disablement bounds the chain by itself: each 401 removes an account for the process lifetime, so it can never exceed the three accounts that exist.
- For an explicit `-kN` route, or when no eligible account remains, return local 502 `upstream_auth_failure`.
- Never relay the upstream 401, its body, or its `WWW-Authenticate` header to the client.
- Exclude the account from subsequent base-alias requests.
- Explicit aliases targeting it return local 503 on later requests.

Immediately means before the bounded drain and before the terminal transaction, not merely before the retry. Those two together are a window in which every concurrent selection can still hand out a credential this process has already watched upstream refuse, and each dispatch that leaves through it spends an RPM slot and a share of some request’s four-dispatch budget on a certain failure. The same ordering applies to a 429 for the same reason. The mutation is memory under a mutex this design already forbids holding across I/O, so moving it to the front of the failure path costs nothing at all.

Relaying that 401 downstream is the obvious alternative and it is wrong twice. An upstream 401 judges the credential the proxy presented, not the one the client presented, so it is the same kind of response as the redirect of §13.2: an instruction addressed to a party that is not the client, which the client cannot act on. It also collides with the proxy’s own contract, where 401 means the proxy key was rejected, so an OpenAI-shaped SDK reports an invalid API key to a caller whose key is valid and abandons the conversation. And it throws the router away at the moment the router is most useful: with one credential revoked and two healthy, roughly a third of new unpinned sessions would fail on a fault the proxy had fully understood and could have hidden.

The account remains disabled for the process lifetime. Correcting the key requires restart, which is also how a renewed subscription on an unchanged key is put back into rotation. Nothing inside the process re-tests a disabled account, on a timer or on a request.

An upstream 403 is non-retryable and relayed unchanged, but it does not disable the account on status alone. 401 says the credential was not accepted; 403 says this request was not permitted, which may be about a model outside the plan, a policy decision, or anything else specific to what was asked rather than to who asked. Disabling on 403 destroys a third of the capacity for the process lifetime on evidence that does not support it, and does so at exactly the moment one model starts refusing requests on every account in turn.

If the provider later documents a stable credential-specific error code carried under 403, a bounded projection of that code alone, retaining no body, may classify it as a credential failure. Until such a code exists and is documented, status is all there is, and status is not enough.

That projection is conditional, and the fact it waits on is recorded rather than assumed. §31 Phase 0 sends a deliberately bad credential and records the status upstream answers with. If that status is 401, the rule above stands exactly as written and no projection ships. If it is 403, disablement never fires on status alone, and the projection stops being optional: Phase 0’s gate requires the documented code before Phase 5, and the projection ships in Phase 5 reading that exact code, classifying on it alone, retaining no body, and leaving every other 403 relayed unchanged and non-retryable as the paragraph above and the §21.2 row state.

### 20.2 Rate-limit failures

On upstream 429, under the coordinator lock and before the response is drained or anything is written to SQLite:

- Add its timestamp to that account’s recent-429 history, pruning entries older than 60 seconds.
- Parse `Retry-After` when valid and advance that account’s gate deadline by the delay it states, clamped to ten minutes. Delta-seconds states that delay directly. An HTTP-date states an instant on upstream’s clock, so the delay is that instant less the `Date` header of the same response, which is the only other instant this proxy holds that was read from the same clock. Valid therefore means parseable and, for the absolute form, derivable. A header that is absent, unparseable or underivable, and one whose derived delay is not positive, advances the deadline to one second after receipt.
- On the third 429 for that account within 60 seconds, additionally move it to `cooling_down` and floor the gate deadline at 60 seconds out.

Then, outside the lock:

- Record the dispatched attempt, storing the parsed delay as `upstream_retry_after_s`, unclamped, because the log should hold what upstream said rather than what the proxy decided to do about it. A delta is stored as sent. An HTTP-date is stored as its derived distance from that response’s own `Date`, which is the only form comparable across rows and is the number the gate itself used, and a date at or before that `Date` stores zero. An HTTP-date the response gave no usable `Date` for stores nothing, because no delay was derived and the fallback the gate took was not upstream’s statement.
- Count it in local RPM.
- Permit rate-limit retries within budget.
- Prefer a different account immediately for an unpinned/base route’s next dispatch. A `Retry-After` from one account is a statement about that account and must never delay a dispatch to a different one.
- Keep explicit aliases on their named account.

Gate expiry:

- Expire lazily.
- Clear the recent-429 history only when a gate the threshold opened expires.

Deriving the absolute form against the local wall clock is the obvious reading and it subtracts two machines. Local time five minutes ahead of upstream turns a date one minute in upstream’s future into a negative delay, which floors at zero, stores zero, and lets the next dispatch walk straight back into the window upstream had just closed; local time behind upstream produces a delay upstream never asked for, long enough to suppress an otherwise viable retry through the runway rule of §21.1. The two operands of a duration have to come from one clock, and the `Date` header is the one instant that arrives on the same response from the same clock as the `Retry-After` beside it. An origin server is required to send it, so the derivable case is the ordinary one; where it is missing the honest answer is that the header stated nothing this proxy can act on, which is the case the one-second floor already covers. That floor is also what a non-positive derived delay takes, because a 429 whose stated delay has already elapsed is upstream declining to name one, and the reason the fallback exists is to stop a burst of concurrent requests from re-entering a window that just closed.

One deadline carries both effects, and what distinguishes them is which rule advanced it. A single 429 gates its account for as long as upstream asked, or for one second when it asked for nothing, which is what stops a burst of concurrent requests from walking straight back into a window that just closed. The third 429 inside a minute opens the longer circuit: it moves the account to `cooling_down`, floors the deadline at 60 seconds, and is the only thing whose expiry clears the recent-429 history. That scoping is load-bearing rather than tidy, because a one-second gate treated as a cooldown expiry would clear the history on the first 429 and the threshold could never be reached at all.

A second, shorter deadline alongside the cooldown was the alternative and is not taken. Selection treats the two identically: each is a deterministic blocker with a known reopening instant, which is exactly what §19.1 computes its stall against, so a second field would add a second expiry, a second skip reason, and a second place for that computation to be wrong, in order to express a distinction no code path acts on.

This tolerates isolated upstream rate responses while preventing repeated pressure on a genuinely closed window, and it keeps a 429 from delaying a dispatch to an account that never sent one.

A 429 is account-specific backpressure and an operational signal, not traffic the design goes looking for. Since the local ceilings are not known to sit below upstream’s, this path may turn out to be the binding rate control or may almost never fire, and which one it is will be visible in the log rather than decidable here. The cooldown threshold and duration are the first constants to re-derive from that log once real traffic exists. They are re-derived by the same owner and under the same gate as the two per-account ceilings of §9.2, both of which are stated in §30.10, and a re-derivation that changes them is an edit to this section.

### 20.3 Server and transport failures

5xx, timeouts, and transient network failures do not globally disable an account.

Reason:

- They may reflect model-wide or provider-wide conditions rather than credential health.
- Disabling all accounts during a common provider outage would amplify failure.
- The current logical request may prefer another account, but account health remains enabled.

A retryable 5xx that carries a valid `Retry-After` is upstream saying when to come back, and scheduling a guessed 250 milliseconds in place of a stated delay is the wrong order. The value becomes the minimum delay for a retry that targets the account which sent it, exactly as §21.3 already treats it for 429, and it is persisted as `upstream_retry_after_s` under the same derivation and the same unclamped rules. It gates nothing else: it neither advances that account's gate deadline nor counts toward the cooldown circuit, because a 503 may describe the provider rather than the credential, which is the reason this whole section leaves health alone. A retry that moves to a different account ignores it entirely, for the reason §20.2 gives about a 429. A stated delay longer than the remaining runway suppresses the retry through the ordinary deadline rule and is recorded as exactly that.

### 20.4 Request failures

400, 404, 409, 422, and other non-authentication 4xx responses:

- Are non-retryable.
- Do not alter account health.
- Are relayed unchanged.

## 21. Retry policy

### 21.1 Global budget

- Maximum four dispatched attempts per logical request.
- Selection skips do not count as attempts.
- Queueing and backoff consume the same ten-minute logical deadline.
- Each account-selection phase has its own 60-second ceiling but never extends the logical deadline.
- No retry begins unless enough deadline remains to acquire and dispatch. "Enough" is five seconds: after the selection delay and the backoff are subtracted, at least that much logical deadline must remain before another dispatch is admitted. An unstated threshold is a decision made silently at the keyboard, and the failure it produces is a dispatch that spends an RPM slot on a request certain to expire mid-flight.
- Expiration of the ten-minute logical deadline is terminal; it is not treated as a fresh retryable attempt timeout.

### 21.2 Classification table

| Failure | Retry | Per-class budget | Next-account preference | Account health |
| --- | --- | ---: | --- | --- |
| Upstream 429 | Yes | Up to 3 retries | Different eligible account first | Count toward cooldown |
| Upstream 5xx other than 504 | Yes | Up to 2 retries | First retry same account; second retry another | No global disable |
| Upstream 408 | Yes | Up to 2 retries | First retry same account; second retry another | No global disable |
| Temporary DNS/dial/reset before response | Yes | Up to 2 retries | First retry same account; second retry another | No global disable |
| Dial or TLS-handshake timeout | Yes | Up to 2 retries | First retry same account; second retry another | No global disable |
| Precommit response-body read failure | Yes | Up to 2 retries | First retry same account; second retry another | No global disable |
| Upstream 504 | Yes | Up to 2 retries | Different eligible account first | No global disable |
| Upstream 401 | Flexible routes only | One per remaining eligible account | Different eligible account only | Disable immediately |
| Upstream 403 | No | 0 | None | No change |
| Other upstream 4xx | No | 0 | None | No change |
| Upstream 3xx or 101 | No | 0 | None | Local invalid-upstream response |
| Invalid URL/protocol/TLS certificate | No | 0 | None | No account change |
| Client cancellation/disconnect | No | 0 | None | No account change |
| Overall logical deadline | No | 0 | None | No account change |
| Body read failure after commitment | No | 0 | None | No account change |
| Downstream response already committed | No | 0 | None | Based on originating failure only |

Mixed failures remain subject to both their class counters and the global maximum of four dispatches.

The same-account first retry for an isolated 5xx/408/transient connection failure preserves account-local prefix cache when the failure is a short blip. It is a preference, not a forced route: if that account is cooling or lacks capacity, another eligible account may proceed. A 429 explicitly says the account is saturated, so it prefers another account immediately.

A processing timeout is a status from upstream, not a clock in the proxy. There is no client-side response-header timeout, deliberately, so the only thing that can report one is upstream itself, and a 504 says the work queued behind this account is not moving. That is account-specific in the way a plain 500 is not, which is why it moves away immediately while the rest of the 5xx class tries the same account once.

### 21.3 Backoff

Rate-limit retry:

- Exponential base delays of 1, 2, and 4 seconds.
- Equal jitter between one-half and the full base delay.
- A valid `Retry-After` becomes the minimum delay only when the next attempt targets the account that sent it.
- When the next attempt targets a different account, no backoff derived from the failed one is applied at all. Atomic selection and its own capacity waits pace the retry, and the account that answered 429 stays blocked by its gate deadline. The exponential delays exist to space repeated attempts against one closed window, not to slow a move away from it.
- Delay is capped by the remaining logical deadline.

Timeout/server retry:

- First retry: approximately 250 milliseconds with equal jitter.
- Second retry: approximately 1 second with equal jitter.
- A valid `Retry-After` on the failed 5xx becomes the minimum delay, and only when the next attempt targets the account that sent it.
- Delay is capped by the remaining deadline.

Credential failover:

- No delay at all. The account that answered 401 is already disabled, so the next dispatch necessarily targets a different one, and nothing about a rejected credential improves while the request waits.

All waiting:

- Uses a timer and `select` on context cancellation.
- Holds no account lease.
- Holds no mutex.
- Is deterministic under a fake clock and injected random source in tests.

### 21.4 Account choice on retry

For a base alias:

- Apply the class-specific preference in the table above.
- Same account is a preference that still passes through normal atomic admission.
- Different account means visiting other eligible accounts before reconsidering the failed account.
- If no preferred account is eligible, normal capacity waiting and the 60-second acquisition ceiling apply.
- A different account is logged as a spill when a live session pin existed.
- Successful alternative completion re-pins the session.

For an explicit account alias:

- Retry only the named account.
- Never spill.

### 21.5 Intermediate response handling

A retryable HTTP response is not committed downstream.

Before retry:

- Observe only bounded usage metadata.
- Drain at most 64 KiB.
- Close the response body.
- Update account health.
- Release the lease.

The health update precedes the lease release, and the order is not cosmetic. §20.1, §20.2 and §12 step 21 are the sections that define it, and a waiter woken by the release must observe the account's post-response health rather than the state it had before this dispatch returned. Reversing the two lets a woken waiter select an account that this very response has just put into cooldown, which is the case a cooldown exists to prevent.
- Append the attempt row.
- Back off.

The final exhausted response is relayed unchanged, including its entire body.

### 21.6 Response-read failures

If an upstream response is accepted but fails while reading:

- Before downstream commitment: classify and retry if the failure budget permits.
- After downstream commitment: log truncation and abort the HTTP response.
- Never concatenate a replacement response to partial data.

### 21.7 Ambiguous-send retries

A transport failure can occur after upstream received and began processing a request but before the proxy received a usable response. Retrying may therefore duplicate upstream generation work.

The proxy accepts this tradeoff because:

- Chat completion generation itself has no proxy-side external side effect.
- The proxy never executes returned tool calls; the client sees and may act on only the final relayed response.
- Availability after an ambiguous transport failure is more valuable than avoiding duplicate flat-rate upstream computation.
- Every dispatch is independently rate-accounted and logged.

The proxy neither invents nor forwards an idempotency key. One supplied by a client is dropped like any other header outside the allowlist, and is recorded as a drop. Nothing in the fixed consumer set sends one, so carrying it would be a forwarding rule written for a caller that does not exist.

## 22. Proxy-generated errors

Local errors use the OpenAI error envelope:

- `error.message`
- `error.type`
- `error.param`
- `error.code`

Messages are stable and sanitized.

| Condition | Status | Type/code |
| --- | ---: | --- |
| Bad proxy key | 401 | `authentication_error` / `invalid_api_key` |
| Unsupported method | 405 | `invalid_request_error` / `method_not_allowed` |
| Unknown path | 404 | `invalid_request_error` / `not_found` |
| Non-empty query string | 400 | `invalid_request_error` / `query_not_supported` |
| Invalid session header | 400 | `invalid_request_error` / `invalid_session_header` |
| Body too large | 413 | `invalid_request_error` / `request_too_large` |
| Compressed body | 415 | `invalid_request_error` / `unsupported_content_encoding` |
| Invalid routing envelope | 400 | `invalid_request_error` / `invalid_request` |
| Nesting depth exceeded | 400 | `invalid_request_error` / `json_depth_exceeded` |
| Unknown alias | 404 | `invalid_request_error` / `model_not_found` |
| Global request or memory overload | 429 | `rate_limit_error` / `proxy_overloaded` |
| Temporary account-capacity timeout | 429 | `rate_limit_error` / `account_capacity_timeout` |
| Dispatch-admission store unavailable | 503 | `server_error` / `admission_store_unavailable` |
| Explicit account disabled | 503 | `server_error` / `account_unavailable` |
| Every flexible account disabled | 503 | `server_error` / `account_unavailable` |
| Upstream credential rejected with no eligible account left | 502 | `server_error` / `upstream_auth_failure` |
| Exhausted transport failure | 502 | `server_error` / `upstream_unavailable` |
| Unexpected upstream redirect or upgrade | 502 | `server_error` / `invalid_upstream_response` |
| Overall timeout before commit | 504 | `server_error` / `deadline_exceeded` |
| Recovered panic before commit | 500 | `server_error` / `internal_error` |

An upstream final response is never converted into one of these local errors. A 3xx, a 101 and a 401 are the carve-outs, and none of them is an exception to the rule, because none of them is a final result: each is an instruction to make a different request, addressed to a party that cannot act on it safely. For a 3xx or a 101 that party is the client. For a 401 it is the proxy itself, which is why a 401 surfaces as account disablement plus failover rather than as a relayed status, and why the local code names the credential failure instead of hiding it behind a generic upstream error.

Every authenticated chat response, local or upstream-derived, includes `X-LLMux-Request-ID`.

Every proxy-generated 429 includes `Retry-After` as whole seconds, rounded up and never less than one. Use the earliest known reopening among eligible accounts, whether an RPM window, an account gate, or the post-start dispatch blackout. If the only blockers are in-flight slots with unknowable release times, use one second. A `proxy_overloaded` 429 also uses one second: it is a global handler or memory failure that consulted no account, so there is no account state to derive a reopening from. A disabled-only failure is 503 and has no fabricated reopening time.

## 23. User-visible workflows

### 23.1 Listing models

1. Client sends authenticated `GET /v1/models`.
2. Proxy validates the bearer key.
3. Proxy projects the fixed catalog into the OpenAI model-list shape.
4. Proxy returns 200.
5. No account state, database state, or upstream is touched.

Visible result: stable base and pinned aliases.

### 23.2 Unauthenticated request

1. Client omits or supplies the wrong bearer key.
2. Proxy returns 401 before reading the body.
3. No account is selected.
4. No attempt row is created.
5. A sanitized warning may be written to stderr.

### 23.3 Non-streaming request without session affinity

1. Client requests a base alias.
2. Proxy rewrites only top-level route-owned values.
3. Accounts are shuffled.
4. First eligible account is atomically admitted.
5. Request is dispatched.
6. Final upstream status, headers, and body are relayed.
7. Usage counts are observed if supplied.
8. One terminal dispatch row is appended.

### 23.4 First streaming request in a session

1. Client supplies a new `X-Session-ID`.
2. Account selection and provisional pin creation are atomic.
3. Request is dispatched to that account.
4. SSE bytes are relayed and flushed.
5. Time to the first complete non-empty data event is observed.
6. On full 2xx completion, the pin is confirmed for one hour.
7. The attempt is appended with account and session.

### 23.5 Continued session

1. Client sends the same session ID.
2. The live pinned account is tried first.
3. If admitted, no shuffle occurs.
4. Successful completion refreshes the one-hour TTL.
5. The log captures the requested alias and serving account.

### 23.6 Explicit account alias

1. Client asks for an exact `-k2` alias.
2. Only `k2` is considered.
3. Temporary saturation causes waiting for at most 60 seconds, then local 429 with `Retry-After`.
4. Disabled `k2` causes local 503.
5. No spill is permitted.
6. Successful completion updates any supplied session pin to `k2`.

### 23.7 Saturated session pin with available alternative

1. Session is pinned to `k1`.
2. `k1` is at RPM, in-flight, or cooldown capacity.
3. A skip row records the reason.
4. If reopening is known within five seconds, the proxy waits exactly for it.
5. If reopening is known to be later, the proxy scans alternatives immediately.
6. For in-flight-only saturation, it waits at most five seconds.
7. If `k1` remains unavailable, the proxy acquires `k2` or `k3`.
8. The dispatch is logged as a spill from `k1`.
9. On fully successful completion, session affinity moves to the spill account.

### 23.8 Every account saturated

1. All eligible accounts are locally unavailable.
2. Distinct skip facts are accumulated.
3. The request waits on state notification or the earliest known expiry.
4. No local rate slot is consumed while waiting.
5. First newly eligible account wins atomic admission.
6. Waiting stops after 60 seconds or the earlier logical deadline.
7. The skip facts and terminal selection failure are appended in one transaction.
8. The proxy returns local 429 with `Retry-After`.

### 23.9 Upstream 429

1. Account dispatch returns 429.
2. Response is not yet exposed downstream if retry remains possible.
3. Attempt is logged.
4. Account 429 state is updated.
5. Lease is released.
6. Context-aware backoff occurs.
7. A base route prefers another account.
8. After retry exhaustion, the final upstream 429 is relayed unchanged.

### 23.10 Upstream 5xx or transport timeout

1. Attempt is classified as transient.
2. No downstream response is committed.
3. Attempt row records the failure and retry decision.
4. Lease is released.
5. An initial 5xx/408/transient-network retry prefers the same account to retain cache.
6. An upstream 504 prefers a different account.
7. Retry occurs within class/global budgets and the original logical deadline.
8. Exhausted HTTP response is relayed unchanged.
9. Exhausted transport failure becomes local 502 or 504.

### 23.11 Upstream authentication failure

1. Upstream returns 401.
2. Account is disabled immediately.
3. Pins to that account are removed.
4. A flexible request retries immediately on another eligible account, and when one of them succeeds the client sees no sign that a credential failed.
5. When no eligible account remains, or the route is an explicit alias, the client receives local 502 `upstream_auth_failure` rather than the upstream 401.
6. Later base routes avoid the account.
7. Later explicit routes to it return local 503.

### 23.12 Malformed or unsupported request parameter

- Malformed routing envelope: local rejection because routing is impossible.
- Well-formed request with an unsupported parameter: forwarded unchanged.
- Upstream decides whether the parameter is accepted.
- Upstream response is relayed unchanged.
- Proxy health is not affected by ordinary request failures.

### 23.13 Partial streaming failure

1. A final stream is committed.
2. Some events reach the client.
3. Upstream read or downstream write fails.
4. Proxy cancels/closes the opposing side.
5. No retry occurs.
6. Attempt records commitment and truncation/disconnect.
7. Token counts remain null unless a complete usage object was already observed.
8. For upstream read failure with a live downstream client, the proxy aborts the HTTP response so the client observes a transport-level incomplete response rather than a clean EOF.

### 23.14 Client cancellation

- Request context cancels.
- Waiting, retry backoff, or upstream dispatch ends promptly.
- Active upstream body is closed.
- Lease is released.
- If dispatched, a terminal attempt row records cancellation.
- If cancellation arrives while the phase is still waiting for an account, that phase appends its deduplicated skips and one terminal selection-failure row all the same. Silence is owed to the client that has gone, not to the log.
- No new response is attempted after the client disappears.

### 23.15 `eod` request without session file

1. `eod` sends a normal non-session chat request.
2. Proxy assigns its own logical request and attempt IDs.
3. Account, alias, latency, outcome, and reported token counts are stored.
4. The final insert is attempted before the handler returns.
5. This remains the durable evidence even though the caller has no session file.

### 23.16 Restart during active conversations

- Completed session pins from the preceding hour are restored.
- No dispatch leaves for one full rolling window after the restart, so no window can hold dispatches admitted by two different processes.
- Active unfinished attempts may have no terminal row after a hard crash; their admission rows survive as the record that they started.
- No health probes run.
- Disabled state resets so corrected credentials can be tested by real traffic.

## 24. Failure modes and required behavior

### 24.1 Startup failures

| Failure | Behavior |
| --- | --- |
| Missing proxy key | Fatal startup |
| Proxy key shorter than 32 bytes | Fatal startup |
| Proxy key equal to an account key | Fatal startup |
| Missing affinity key | Fatal startup |
| Affinity key shorter than 32 bytes | Fatal startup |
| Missing account key | Fatal startup |
| Duplicate account keys | Fatal startup |
| Invalid listen address | Fatal startup |
| Non-loopback listen address | Fatal startup |
| Invalid catalog | Fatal startup |
| Relative database path | Fatal startup |
| Store lock held by another live process | Fatal startup |
| Missing database directory | Fatal startup |
| Database directory writable by group or others | Fatal startup |
| Insecure existing database permissions | Fatal startup |
| SQLite open failure | Fatal startup |
| Unsupported future schema | Fatal startup |
| Migration failure | Transaction rollback and fatal startup |
| Database not writable | Fatal startup |
| Bind failure | Close initialized resources and exit nonzero |

No startup failure triggers an upstream request.

### 24.2 Request-envelope failures

| Failure | Upstream called? | Response |
| --- | --- | --- |
| Bad auth | No | 401 |
| Unsupported method | No | 405 |
| Unknown path | No | 404 |
| Oversize headers | No | Standard bounded server failure |
| Body slower than the read timeout | No | Standard bounded server failure |
| Body read error | No | 400 unless client vanished |
| Oversize body | No | 413 |
| Compressed body | No | 415 |
| Invalid JSON | No | 400 |
| Duplicate/missing model | No | 400 |
| Unknown alias | No | 404 |

### 24.3 Capacity failures

| Failure | Behavior |
| --- | --- |
| Pinned account with known reopening inside grace | Wait exactly until reopening or notification |
| Pinned account with known reopening after grace | Spill scan immediately |
| Pinned account blocked only by in-flight work | Wait at most five seconds, then spill scan |
| Explicit account saturated | Wait only for that account, at most 60 seconds |
| Explicit account disabled | Immediate local 503 |
| All flexible accounts saturated | Wait for any account, at most 60 seconds, then local 429 |
| Post-start dispatch blackout in force | Wait for it to lift, within the 60-second acquisition ceiling, then local 429 with `Retry-After` |
| All flexible accounts disabled | Immediate local 503 |
| Sixty-second acquisition ceiling reached during wait | Local 429 with `Retry-After`, because the capacity is expected back |
| Ten-minute logical deadline reached during wait | Local 504, because the request ran out of time rather than the window |
| Client cancellation during wait | Append the phase’s deduplicated skips and one terminal selection-failure row with outcome `client_canceled`; write no response |

### 24.4 Network and upstream failures

| Failure | Behavior |
| --- | --- |
| DNS timeout | Retry within transport budget |
| Temporary dial failure | Retry |
| TLS timeout | Retry |
| Invalid certificate/protocol | Do not retry |
| Connection reset before response | Retry |
| Upstream 504 | Retry on another account |
| 429 | Retry and update rate-limit health |
| 5xx | Retry |
| 401 | Disable account; flexible routes fail over to another eligible account; local 502 when none remains |
| 403 | Relay unchanged, no retry, account untouched |
| Other 4xx | No retry |
| 3xx or 101 | No redirect or upgrade; local 502 before commitment |
| Read error before commitment | Retry if budget permits |
| Read error after commitment | Log truncation and abort the downstream response |

### 24.5 Persistence failures

| Failure | Behavior |
| --- | --- |
| SQLite busy under five seconds | Wait within busy timeout |
| SQLite remains busy for a terminal write | Sanitized stderr error; preserve client result |
| SQLite remains busy for an admission write | Cancel the reservation; local 503; no dispatch |
| Disk full during a terminal write | Sanitized stderr error; continue serving |
| Disk full during an admission write | Local 503 for every new dispatch; already-admitted attempts finish |
| Lifecycle start row cannot be appended | Fatal startup |
| Lifecycle stop row cannot be appended | Sanitized stderr error; exit status unchanged |
| Local rejection row cannot be appended | Sanitized stderr error; the client result was already written and is unchanged |
| Runtime corruption error | Sanitized high-severity log; continue only where connection remains usable |
| Store becomes unusable | Repeated append failures remain visible; no in-memory unbounded queue |
| Crash before terminal phase transaction | Active attempt result and pending skip rows may be absent; its admission row is not |
| Crash after commit | Committed row remains durable |
| One row in a phase batch violates a constraint | Roll back the entire phase transaction; emit one sanitized error |

A store that cannot accept an admission stops the proxy from originating upstream traffic while letting admitted traffic finish. This is the one place where evidence outranks availability, and it is a deliberate reversal of the rule above it. What it protects is completeness rather than rate correctness, which §16.3 carries on the monotonic clock instead: the admission ledger is the only thing that can bound what left this process, since every dispatch has a row and nothing without a row was ever permitted to leave, and a hole in it is indistinguishable from a hole in reality. A missing terminal row already means one stated thing, that the process never learned how an attempt ended. A missing admission row would mean either that the store was briefly unavailable or that a dispatch happened which nothing recorded, and no reader could tell those apart.

There is no fallback JSONL file, alternate database, or memory log.

### 24.6 Clock anomalies

- Durations use monotonic time.
- Persisted timestamps use UTC wall time.
- In-process rate limiting uses monotonic time.
- Restart recovery necessarily uses wall timestamps, and session affinity is the only thing recovered from them.
- Session affinity expiry is therefore wall-clock too, by §16.2, so recovery converts nothing between clocks.
- Future recovered timestamps are clamped to startup time and produce a warning.
- Backward wall-clock changes cannot make monotonic durations negative.
- No duration is computed by subtracting two persisted instants. Every duration this store holds was measured monotonically inside the process that wrote it, and every persisted instant exists to say when something happened rather than how long it took.
- No live deadline is seeded from a persisted instant measured on a different clock than the deadline runs on.

The last two are the general form of three separate defects this document has already had to correct, each of which read as an ordinary sentence until the sequence of events was written out: a first-event metric anchored in front of a durable write, a rolling window anchored in front of a commit that had been inserted before the boundary it was named for, and a monotonic window seeded at startup from a wall-clock column. Stating them as rules is what makes the next instance visible without reconstructing the failure, and §28 is where a violation fails rather than being noticed later by a reader.

There is exactly one exception, and it is written down rather than left to be discovered. Pin recovery in §16.3 subtracts `logical_elapsed_us`, a monotonic duration, from `finished_at_us`, a wall instant, to order two completions of one session. It buys an ordering that matches the live sequence guard without a column, its exposure is a clock step landing between two completions of one session inside one hour, and its cost when that happens is which of two accounts a recovered conversation resumes on. It is stated there as an exposure rather than as a guarantee, and it is the only place in this document where the rule above is knowingly broken.

### 24.7 Internal panic

A top-level handler recovery boundary:

- Executes cleanup defers.
- Releases account leases.
- Cancels upstream work.
- Returns local 500 only if the response is uncommitted.
- Aborts the connection/stream if already committed.
- Emits a stack only to sanitized local stderr.
- Does not include bodies or headers in the panic event.
- Recognizes `http.ErrAbortHandler` as an intentional transport abort and re-propagates it without converting it to a 500 or logging a defect stack.

Panics remain defects and must fail tests; recovery only protects process availability.

## 25. Concurrency invariants

The implementation must document and test these invariants:

1. Account/session state has one owner: the coordinator.
2. Every shared-state read or write occurs under the coordinator mutex.
3. No I/O occurs while holding it.
4. Every acquired lease is released exactly once.
5. In-flight never becomes negative or exceeds twelve.
6. No account has more than 60 admitted starts in a rolling 60-second interval.
7. Retries acquire fresh leases.
8. Waiting and backoff hold no leases.
9. Selection skips consume no rate capacity.
10. New-session pin creation and first account admission are atomic.
11. A stale request cannot overwrite a newer session-pin update.
12. Database insertion order does not define request sequence; `sequence_no` does.
13. Shutdown cannot close SQLite while active handlers may still append.
14. Downstream commitment is a monotonic state transition.
15. A committed response can never return to the retry state.
16. Account health mutation and limiter admission use the same account identity.
17. Pinned variants never create new account state.
18. Process logs and SQLite writes occur after coordinator unlock.
19. Observer errors cannot affect response relay.
20. Client cancellation propagates through waiting, backoff, upstream I/O, and database calls where applicable.
21. Every selection phase is bounded by 60 seconds and records one terminal dispatch or selection failure.
22. The first same-session request cannot split its provisional pin across accounts under concurrent arrival.
23. No response header reaches the downstream writer until the final-response state machine commits.
24. A post-commit upstream read failure cannot return normally through the HTTP handler.
25. Pending skip facts are bounded by the fixed account/reason vocabulary and cannot form an unbounded per-request queue.
26. No admission path grants an account an exception to disabled health state.
27. No `http.Client.Do` occurs without a committed admission row, and no admission row is committed without a held reservation.
28. Client cancellation and logical-deadline expiry cannot cancel admission or terminal persistence before its own bounded store timeout.
29. Aggregate request-owned memory never exceeds the configured budget, measured in allocated capacity rather than in bytes received. A request holds one charge, which may only grow while its body is read and is settled once when the read completes, and every charge is released exactly once.
30. An unconfirmed provisional pin with no remaining holders cannot stay live.
31. The rolling window is measured over `http.Client.Do` invocation instants, and a pending reservation occupies a slot from the moment it is granted until it is finalized or canceled.
32. No dispatch occurs during the first full rolling window of a process's life, measured monotonically, so no rolling window can contain dispatches admitted by two different processes.
33. Live accepted client connections never exceed the configured ceiling, so no server goroutine exists for a connection beyond it.

## 26. Security and privacy

### 26.1 Trust boundaries

- Local client to proxy.
- Proxy configuration/environment to process.
- Proxy to Ollama Cloud.
- Process to SQLite.
- Operator/local analysis tool to SQLite.

### 26.2 Credential handling

- Read keys once at startup.
- Keep them only in process memory.
- Never include them in formatted errors.
- Replace client authorization before upstream dispatch.
- Forward only the fixed request-header allowlist.
- Strip proxy/session-specific, cookie, trace, forwarding, and hop-by-hop headers.
- Disable redirects.
- Never serialize configuration structs containing secrets.
- Persist only keyed session digests.
- Do not expose environment diagnostics through HTTP.
- Do not pass secrets as command-line arguments.
- Disable environment proxy discovery on the upstream transport, so no environment variable can choose who receives an account credential.
- Require owner-only permissions on the service environment file and SQLite store.

### 26.3 Fixed upstream and SSRF prevention

- Upstream scheme and host are source constants.
- The client cannot supply an upstream URL.
- No environment variable can interpose a proxy between the process and that host.
- No query string is forwarded, because none is accepted.
- Redirect following is disabled, and a redirect is never handed to the client either.
- No proxy or arbitrary URL endpoint exists.

### 26.4 Local exposure

- Binding is loopback only, enforced at startup.
- Every endpoint requires authentication.
- No debug, pprof, expvar, metrics, or admin listener is enabled.
- SQLite file permissions are restricted.

### 26.5 Logging privacy

Process logs may contain:

- Proxy logical request ID.
- Alias.
- Account label.
- Attempt number.
- Stable outcome/error class.
- Status code.
- Duration.
- Retry decision.
- Names of request headers removed by the allowlist.

They must not contain:

- Request or response bodies.
- Message content.
- Tool arguments.
- Authorization headers.
- Account keys.
- Raw upstream error bodies.
- Any request or response header value, including those of headers named in a drop event.
- Session identifiers, raw or digested, in stderr by default.
- Upstream-generated IDs.
- Currency or cost.

### 26.6 Resource denial controls

- 64 MiB request-body limit.
- 8 MiB non-streaming precommit buffer, after which relay becomes progressive.
- Global ceiling on concurrent admitted chat handlers.
- Ceiling on live accepted client connections, which bounds the per-connection server goroutines that precede handler admission.
- Weighted aggregate budget over request, replay, and precommit memory.
- One body buffer per request, replayed from immutable segments, exact when the length was declared and grown inside its charge otherwise.
- 64 KiB header limit.
- Bounded JSON nesting depth.
- Ten-minute logical request deadline.
- 60-second account-acquisition ceiling.
- Twelve in-flight attempts per account.
- Exact account RPM ceiling.
- Two-minute request-body read timeout.
- Per-write downstream deadline for a stalled consumer.
- Bounded SSE observer.
- Bounded observer decoding, capped on cumulative decoded output rather than on input size.
- Bounded retry drain.
- Maximum four dispatches.
- Bounded deduplicated skip collection.
- Ceiling on live session pins, which are the one allocation that outlives the request creating it.
- No unbounded log queue.
- No per-request unbounded goroutine creation.

## 27. Process observability

### 27.1 Structured logs

Use standard-library structured logging with JSON output to stderr.

Events include:

- Startup complete, carrying version, revision, schema version, and Go toolchain.
- Shutdown requested/completed.
- Fatal configuration/database/listener errors.
- Account disabled.
- Account cooldown entered/expired.
- Request headers removed by the allowlist, at debug level, by name only.
- Runtime attempt-log insert failure.
- WAL growth past its threshold despite checkpoint attempts.
- Recovered handler panic.
- Forced shutdown.
- Recovery clock skew.
- Stream-signal disagreement, as defined by §13.4.
- Pin map at its ceiling, at warn level, as defined by §16.2.

Normal successful attempts need not produce duplicate process-log events because SQLite is the durable attempt record.

### 27.2 Log levels

- Debug: foreground routing decisions during explicit troubleshooting.
- Info: startup, readiness, shutdown.
- Warn: account cooldown, recoverable persistence problem.
- Error: account disablement, repeated store failure, panic, forced shutdown.
- Fatal behavior is implemented by returning an error to `main`, which logs once and exits nonzero.

### 27.3 No added observability endpoints

There is no Prometheus endpoint, tracing exporter, dashboard, pprof server, or UI. Those would expand the exposed surface beyond scope.

The durable SQLite attempt log and stderr lifecycle logs are sufficient for this single-machine service.

## 28. Testing obligations

Tests are executable requirements, not merely coverage exercises.

All time-dependent tests must use either the complete injected clock/timer boundary or Go 1.26 `testing/synctest`. They must not rely on long real sleeps. The one exception is a short black-box binary smoke test whose purpose is to validate real sockets, process signals, and flushing.

The injected clock advances wall time and monotonic time independently, and a test that moves both together proves nothing about which one the code read. Every rule in §24.6 says that some quantity follows one clock and not the other, so the assertion that carries it is always an advance of one while the other is held. This is the shape of test the restart cases had been missing: they stepped the wall clock across a restart, which a new process recomputing its deadlines after the step survives regardless of the clock it chose.

The scripted fake upstream must record the account by the bearer key it actually receives, dispatch start time, live concurrency, request bytes, and cancellation. Limiter invariants must be asserted at this external observation point as well as against coordinator state.

The coordinator additionally carries model-based tests that generate random sequences of reservation, admission success and failure, dispatch, release, cooldown, cancellation, crash and recovery events against a reference model of the same state. Example-based tests cover the interleavings someone thought of, and the failures worth fearing in a component like this one are the interleavings nobody did.

Crash behavior is tested with real subprocesses killed at each boundary that the two commit points create: after coordinator reservation, after the admission commit, after `http.Client.Do` returns, after downstream commitment, and after the terminal insert. A restarted process reading that store must never admit more than the ceiling allows for the window the crash fell in.

Retry classification, response commitment, and account-health transitions are each expressed once as a table-driven conformance matrix, as §1 requires. The unit tests and the full-handler tests read the same table rather than each encoding the rules again, so a row that changes changes both, and a case nobody wrote a handler test for still fails at the unit level instead of being absent from both.

### 28.1 Catalog tests

Verify:

- Seven base aliases.
- 28 total aliases.
- Stable model-list order.
- Correct base-to-upstream mapping.
- Correct reasoning injections.
- Correct inherited injections for all pinned variants.
- Exact account restriction for every `-kN` route.
- No injected `messages` key.
- Duplicate catalog IDs fail startup validation.
- `/v1/models` contains no unregistered alias.
- Account health never changes model-list output.

### 28.2 Body-rewriter unit tests

Cover:

- Minimal valid body.
- Arbitrary top-level field order.
- `messages` before or after `model`.
- Deeply nested message structures.
- Tool calls and tool results.
- Unknown message fields.
- Escaped quotes and backslashes.
- Braces and brackets inside JSON strings.
- Unicode and surrogate escapes.
- Numbers, booleans, and null.
- Empty messages array.
- Missing messages.
- Existing route-owned field.
- Missing route-owned field.
- Duplicate model.
- Duplicate route-owned injection.
- Escaped spellings of `model`, `stream`, and the route-owned injection key, alone and alongside their plain spellings.
- Malformed JSON at every structural position.
- Trailing garbage.
- Top-level arrays/scalars.
- Exact 64 MiB boundary.
- Maximum accepted nesting depth, and the first rejected one.
- Replay from the immutable segments across all four attempts, with no second body-sized allocation.

For every successful rewrite:

- Parse the original and output only for test comparison.
- Assert output model and route-owned values.
- Assert every untouched top-level raw value span is byte-identical, not merely semantically equivalent.
- Assert the relative order of untouched top-level members is unchanged.
- Assert duplicate unknown top-level keys survive unchanged.
- Assert the raw `messages` byte span is exactly identical.
- Assert no message field order changed.
- Assert no `stream_options` or usage-requesting field appears unless the client supplied it.

### 28.3 Body-rewriter fuzzing

Fuzz targets must:

- Generate nested objects and arrays.
- Generate arbitrary valid strings and escapes.
- Generate escaped spellings of the route-owned member names.
- Mutate valid bodies into malformed inputs.
- Generate nesting at, below, and above the depth limit.
- Seed real multi-turn tool-loop shapes.
- Assert no panic and no stack exhaustion.
- Assert successful outputs remain valid JSON.
- Assert `messages` raw bytes remain identical.
- Assert only permitted top-level values change.

Persist minimized crashing inputs as test fixtures.

Allocation behavior is asserted in the deterministic benchmarks and resource tests of §28.18, not here. A fuzz target has no stable per-iteration allocation accounting, so a memory assertion inside one either flakes or is written loose enough to prove nothing.

### 28.4 Authentication tests

Cover:

- Correct bearer key.
- Missing header.
- Wrong scheme.
- Empty bearer value.
- Several credentials in one header field.
- Several `Authorization` header fields, one of them correct.
- Case-insensitive bearer scheme.
- Wrong key of equal length.
- Wrong key of different length.
- Auth failure before body read.
- 401 shape and `WWW-Authenticate`.
- No attempt-log write on auth failure.
- Key/header absence from captured logs.

### 28.5 Rate-limiter unit tests

Use a fake monotonic clock.

Verify:

- First 60 starts in 60 seconds are admitted.
- The 61st is rejected.
- Exact boundary expiration admits correctly.
- Failed dispatches remain counted.
- Retries consume another timestamp.
- Skips do not.
- Separate aliases share the same account count.
- Base and pinned aliases share the same account count.
- Different accounts remain independent.
- In-flight never exceeds twelve.
- Release makes in-flight capacity available.
- RPM expiry requires no background goroutine.
- Cooldown expiry is lazy and correct.
- Double lease release is harmless or detected without underflow.
- A failed admission commit frees the pending RPM slot and the in-flight slot.
- No dispatch occurs when admission persistence fails.
- A pending reservation holds the sixtieth slot against a concurrent caller for the whole time its admission commit runs.
- A deliberately slow admission commit dates its dispatch timestamp at the `Do` boundary and not at reservation, so more than 60 dispatches never fall inside one 60-second interval measured at that boundary.
- No account admits a dispatch before one full rolling window has elapsed since process start, and the first admission after that instant succeeds.
- A crash and restart straddling a saturated window cannot place more than 60 dispatches in any rolling 60-second interval, with the wall clock deliberately stepped forward and backward across the restart.
- Inside one live process, stepping the wall clock forward and backward while monotonic time is held changes neither the contents of the rolling window nor the remaining blackout, so no eligibility decision moves.
- Advancing monotonic time while the wall clock is held expires the window and the blackout at their exact monotonic instants, which is the half of the previous case that proves the implementation is reading a clock at all rather than ignoring both.

### 28.6 Concurrency stress tests

Run both coordinator-only stress and full-stack stress through the HTTP handler into the scripted upstream. Coordinator-only stress uses hundreds or thousands of goroutines against one coordinator.

Assert:

- At most twelve leases per account at every observation point.
- No rolling interval exceeds 60 admissions.
- The fake upstream itself never observes more than twelve live requests for one account key.
- The fake upstream’s dispatch-start timestamps never contain more than 60 starts for one account in any rolling 60-second interval, including under a store fake that delays every admission commit close to its ceiling.
- Requests arriving through different aliases, pinned aliases, sessions, and retries still share those upstream-observed ceilings.
- Concurrent final-slot claims admit only one caller.
- No races under `go test -race`.
- No deadlocks.
- Canceled waiters exit promptly.
- Notification replacement does not lose wakeups.
- New concurrent session requests select one initial account.
- Concurrent pin updates honor arrival sequence.
- No goroutine leaks.

### 28.7 Saturation/spill tests

Cover:

- Pin available immediately.
- Pin frees inside five seconds.
- RPM reopening in four seconds waits approximately four synthetic seconds and remains pinned.
- RPM reopening after 45 seconds scans alternatives immediately instead of waiting five seconds.
- Cooldown reopening inside and outside the pin grace follows the same rule.
- Combined deterministic and in-flight blockers use the correct all-blockers-must-clear time.
- Pin remains blocked and alternative is available.
- Pin and alternatives all blocked.
- Pin disabled.
- Pin cooling down.
- Explicit alias never spills.
- Spill row contains source and destination.
- Successful spill re-pins.
- A request that runs for nine synthetic minutes dates its hour from successful full-response completion and from no earlier boundary, so its pin outlives one that started at handler start, at reservation, at `Do`, or at upstream EOF by the length of the request.
- Upstream-error spill does not re-pin.
- Partial stream spill does not re-pin.
- Older concurrent completion cannot overwrite a newer pin, and a restart after that ordering recovers the same pin the live coordinator held.
- A failed first request in a new session leaves no pin behind.
- A new session arriving while the pin map is full routes unpinned, creates no entry, and evicts nothing.
- Concurrent first requests share one provisional pin, and it survives while any of them is still running.
- The last concurrent first request to fail removes the provisional pin.
- Deadline during grace.
- Deadline after spill becomes allowed.
- Sixty-second account-acquisition expiry returns 429.
- `Retry-After` is the rounded-up earliest known reopening, and falls back to one for in-flight-only saturation.
- Selection failure transaction contains deduplicated skips and one terminal failure row.
- Cancellation during a selection wait still leaves one terminal selection-failure row, with outcome `client_canceled`.

### 28.8 Health-state tests

Verify:

- First upstream 401 disables immediately.
- 403 is relayed, is not retried, and leaves account health alone.
- Pins to disabled account are removed.
- A flexible request that hits 401 completes on another eligible account, and neither the upstream 401 nor its `WWW-Authenticate` header reaches the client.
- An explicit-alias 401, and a 401 with no eligible account left, return local 502 without relaying the upstream response.
- Subsequent base requests skip disabled account.
- Explicit route fails locally.
- No code path re-enables a disabled account within one process lifetime.
- One or two 429 responses gate their account for the advertised delay, or one second, without entering the cooldown circuit or clearing the recent-429 history.
- Third 429 in 60 seconds enters cooldown.
- Old 429 timestamps expire.
- Delta-seconds are honoured as sent, and an HTTP-date is derived against the `Date` header of the same response and is unmoved by stepping the local wall clock in either direction.
- An HTTP-date on a response carrying no usable `Date`, and one whose derived delay is not positive, both gate the account for one second; the first stores no advertised delay and the second stores zero, because one of them is upstream saying nothing and the other is upstream saying now.
- Cooldown is clamped.
- Cooldown wakes waiters.
- A 401 or 429 is applied to account health before the response is drained, so a concurrent selection cannot hand out a credential or a window the process has already seen refused.
- A valid `Retry-After` blocks only the account that sent it.
- Another account is selected without waiting out that `Retry-After`.
- 5xx does not disable an account.
- Request 4xx does not affect health.
- Restart clears disabled state without probing.

### 28.9 Retry-state tests

Use a scripted fake upstream.

Cover:

- 429 then success.
- Three 429 retries then final 429.
- 500 then same-account success.
- Two 5xx failures move from same-account preference to another account.
- Same-account retry falls back to another when the preferred account lacks capacity.
- Upstream 504 prefers another account.
- A 503 carrying `Retry-After` delays a same-account retry by at least the stated value, stores it unclamped, leaves account health and the cooldown circuit untouched, and delays a retry that moves to another account not at all.
- The independent-clock cases of §28.8 hold on a 5xx too, in the delay imposed on a same-account retry, in the value stored, and in whether the runway rule suppresses that retry.
- Precommit response read failure retries.
- Mixed 429 and 5xx respecting the global cap.
- 400 with no retry.
- 401 disables its account and fails over to another eligible one without backoff, and 403 has no retry and no disable.
- TLS permanent error with no retry.
- Client cancellation during backoff.
- Deadline during backoff.
- A retry left with exactly the five-second runway once its backoff and selection time are subtracted is admitted, and one a single monotonic tick short of it is suppressed as `suppressed_deadline`, reaching no `Do` and consuming no RPM slot. The case above cannot fail on this boundary, because it ends the request while the wait is still running: an implementation that dispatches with one second left and expires mid-call passes it and spends a slot on a request certain not to finish.
- Overall ten-minute logical deadline is never renewed or retried.
- Retry account preference.
- Explicit alias retry remains on one account.
- Every retry acquires a new limiter slot.
- No lease is held during backoff.
- Intermediate response bodies are boundedly drained and closed.
- Final exhausted upstream body is relayed unchanged.
- An ambiguous transport failure followed by retry creates two independently rate-accounted attempt rows.
- A client-supplied idempotency header is dropped and counted like any other unlisted header, and the proxy never invents one.

### 28.10 HTTP handler tests

Use `httptest` servers and recorders.

Verify:

- Exact route/method handling.
- OpenAI error envelope.
- Header filtering.
- Client authorization never reaches upstream.
- Session header never reaches upstream.
- `X-LLMux-Request-ID` is present on local and relayed responses alike, and an upstream header of that name never survives.
- Account authorization does reach upstream.
- Each allowlisted end-to-end header is preserved.
- Cookies, forwarding headers, trace headers, and arbitrary custom headers are stripped.
- A stripped header is counted on its attempt row and named in a debug event, and its value appears nowhere.
- Hop-by-hop headers and the response state/routing strip list are removed.
- Query strings are rejected locally.
- A client that sends no `User-Agent` produces no synthesized default upstream.
- Several `X-Session-ID` fields are rejected locally.
- One dispatch cannot be transparently replayed by the transport.
- A hostname, a wildcard, and a non-loopback literal each fail startup.
- An upstream 3xx, 101, or `Alt-Svc` can redirect neither the proxy nor the client, and neither credential follows one.
- Environment `HTTPS_PROXY` and `ALL_PROXY` do not affect upstream routing.
- Upstream response headers past the configured bound fail the attempt before commitment.
- Redirects are not followed.
- Upstream status and body are byte-identical.
- Compression is not transparently changed.
- Unknown parameters reach upstream.
- Message arrays are byte-identical at upstream.
- No `/v1/models` request reaches upstream.
- A staged timing fixture advances the monotonic clock by a distinct nonzero amount in each phase of one request: pre-selection handler work, the selection wait, the admission commit, the upstream call before its first event, the remainder of the response, and the terminal relay. It asserts that `selection_wait_us` runs from phase start to lease acquisition, `attempt_duration_us` from `Do` to response close, `time_to_first_event_us` from `Do` to the first qualifying event, `logical_elapsed_us` from handler start to the row's own terminal boundary, and `event_at_us` at the wall instant of `Do` rather than of the reservation before it. The admission commit's own duration appears in none of the upstream-call figures.

Every phase of that fixture is a different number on purpose, and it is what the rest of this section cannot do. A timing test whose intervening phases take no synthetic time passes just as happily against an implementation that starts `attempt_duration_us` at the reservation, starts `logical_elapsed_us` at selection, folds the admission commit into upstream latency, or fills `selection_wait_us` from handler start, because every one of those wrong anchors returns the same value as the right one when nothing happens between them. With a five-second admission commit and a first event 100 ms after `Do`, the regressed implementation reports 5.1 seconds and the generic assertion still holds. Distinct nonzero phases are what turn each of those into a different number instead of the same one.

### 28.11 Streaming integration tests

Use a controllable SSE upstream.

Test:

- Event-by-event relay.
- Flush visibility before completion.
- Comments and blank lines.
- `[DONE]`.
- Fragmented TCP reads.
- Several events in one read.
- A line spanning many reads.
- One line over the observation cap.
- Slow upstream, including an idle period longer than the downstream write deadline, which must not end the stream.
- Slow downstream.
- A downstream consumer that stops reading entirely reaches the write deadline and releases its lease.
- Client disconnect.
- Upstream truncation.
- EOF before the first body byte remains uncommitted and retries when eligible.
- Read error before the first body byte remains uncommitted and retries.
- The first non-empty chunk is relayed exactly after commitment.
- First-event timing at the first complete non-empty data event.
- Final usage extraction.
- No usage.
- Malformed usage.
- A nested value named `usage` that is not the top-level object.
- A request marked `"stream": true` whose upstream content type says otherwise still relays progressively.
- A gzip response relays byte-identically while usage and first-event timing are observed through the bounded decoder.
- A response in an encoding the observer cannot decode, and one that trips the decoded-output cap, relay byte-identically with null counts, and record `unsupported_encoding` and `limit_exceeded` respectively.
- An observer that falls behind the relay abandons observation at its bounded buffer rather than delaying a downstream write.
- A decompression bomb abandons observation at the output cap without slowing or altering relay.
- No proxy-injected `stream_options`.
- No retry after first committed byte.
- Post-commit upstream read failure produces a raw-client transport error rather than a clean completed response.
- Raw HTTP/1.x abort behavior is tested against the one protocol the local server enables.
- Recovery middleware re-propagates `http.ErrAbortHandler`.
- Exact output bytes despite observer failure.

Timing assertions must use synchronization points rather than fragile sleeps.

### 28.12 Non-streaming integration tests

Cover:

- Successful JSON response.
- Response below 8 MiB remains uncommitted until complete.
- Response exactly at the precommit boundary.
- Response above 8 MiB transitions to progressive relay without byte changes.
- Usage at different object positions.
- Unknown response fields.
- Malformed response JSON relayed unchanged.
- Response read error before commitment.
- Response read error after commitment.
- Client disconnect during write.
- Exact status/header/body preservation.
- Precommit buffers are released after response completion and do not scale with total over-threshold body size.
- A fully buffered response releases its account in-flight lease before the downstream write, and a slow reader cannot hold account capacity.
- A precommit allowance denied by the aggregate gate relays the same bytes progressively rather than failing the request.

### 28.13 SQLite tests

Use real temporary SQLite files.

Verify:

- Empty-database creation.
- New database is pre-created with mode `0600`.
- Existing symlink and insecure file modes are rejected.
- Every migration path.
- Future-version refusal.
- Correct schema constraints.
- Concurrent append serialization.
- Unique ID handling.
- Update/delete triggers.
- Index presence.
- `EXPLAIN QUERY PLAN` on the session recovery query, asserting the intended index and no full table scan.
- `PRAGMA foreign_keys` is on for every connection the pool hands out, and an orphan dispatch row is refused by the live connection rather than only by the schema text.
- Passive checkpoints run on the maintenance connection, and an admission commit issued while one is running does not queue behind it.
- WAL growth and its warning under a deliberately held external reader.
- Null token counts.
- Full token counts.
- Every value of the `usage_observation` vocabulary, and its absence on skip and selection-failure rows.
- Session digest stability across restart, and absence of the raw header.
- All three record kinds.
- One `unrouted_request` row for each envelope rejection, overload, and unknown alias, carrying the identifier the client was given.
- No `logical_request_id` present in both `unrouted_request` and `attempt_log`, across the full matrix of local rejections, selection failures, and dispatched requests.
- Selection and attempt numbering.
- Aggregate skip counts.
- Upstream retry delay persisted on 429 and 5xx dispatch rows whose response carried the header, in the delta form and in the HTTP-date form paired with a usable `Date`, unchanged by local wall-clock skew in either direction, and absent everywhere else, including on a row whose HTTP-date had no `Date` to be derived against.
- Terminal capacity-failure rows.
- One phase batch commits atomically.
- A deliberate bad row rolls back its whole phase batch.
- No prompt/completion columns.
- No cost/currency columns.
- Startup session recovery.
- Process start and stop rows paired by instance identity, a `process_elapsed_us` that stays correct and non-negative across forward and backward wall steps inside the run, and the unmatched start row left by a killed subprocess whose successor starts and stops under an earlier wall time than the one that died.
- With the two clocks advanced independently, every persisted instant follows UTC wall time and every persisted duration follows monotonic time and stays non-negative, which is what §24.6 states and what nothing previously could have failed on.
- Startup reads no rate state from `dispatch_admission`, so a store seeded with recent admissions changes nothing about when the first dispatch may leave.
- Admission rows survive a subprocess killed immediately after their commit.
- Every named query recipe of §30.3 parses and runs against a seeded store and returns the shape its name promises, so a schema change that invalidates one fails here rather than in front of an operator.
- A dispatch whose admission commit runs long enough to place its reservation and its `Do` on opposite sides of a minute boundary is counted by the dispatch recipes in the interval the call fell in and by the admission recipe in the interval the reservation fell in, and a subprocess killed between the two contributes to the second and to the unmatched-admission figure but not to the first.
- Busy timeout behavior.
- Disk/permission failures where platform support permits.
- Store close ordering.
- WAL-backed concurrent reading.

### 28.14 Privacy tests

Representative requests and responses must contain unique marker strings in:

- User messages.
- System messages.
- Tool arguments.
- Tool output.
- Assistant completion.
- Upstream error body.
- Authorization headers.
- Cookies, custom trace headers, and forwarding headers.

After requests:

- Search SQLite, WAL, and process logs.
- Assert none of the marker text exists.
- Assert account keys and proxy key do not exist.
- Assert token counts and allowed metadata do exist.
- Assert no raw session identifier exists, and that its versioned digest does.
- Assert stripped header markers never reach the upstream.
- Assert a dropped header’s name may appear in a debug event while its marker value appears in no log and no database file.
- Assert JSON parse errors and panic logs contain positions/classifiers but not nearby body excerpts.

No test may create temporary prompt spool files.

### 28.15 Restart tests

Exercise:

- Successful session recovery within one hour.
- Expired session not recovered.
- Newest successful account chosen after spill.
- Two successful requests of one session that finish out of arrival order recover the account of the later arrival, matching what the live sequence guard chose.
- Two rows of one session with equal derived arrival instants recover the same account on every run, resolved by the stated tie-break rather than by the order the rows came back in.
- Failed spill ignored for recovery.
- A restarted process refuses every dispatch until one full rolling window has elapsed, whatever the store holds.
- In-flight resets to zero.
- Disabled health resets.
- No startup upstream requests.
- Clock-skew clamping.
- A pin recovered at startup expires at the same wall instant as one established live, and a wall step after recovery moves both alike, so nothing on that path converts a stored instant into a deadline on another clock.
- A second process pointed at the same database fails startup while the first lives, and succeeds once the first has exited, including after a SIGKILL that gave it no chance to release anything.

### 28.16 Shutdown tests

Verify:

- New connections stop.
- Active stream can complete within grace.
- First signal is graceful.
- Second signal forces cancellation.
- A handler admitted an instant before the first signal, running to its full logical deadline, still lands its terminal row before the shutdown grace expires.
- Waiting account acquisition exits.
- Retry backoff exits.
- Leases release.
- Attempt inserts occur before SQLite closes.
- Idle upstream connections close.
- No goroutine leaks.

### 28.17 Static and quality gates

Every release must pass:

- Go formatting.
- `go vet`.
- Configured `golangci-lint`.
- `go test ./...`.
- `go test -race ./...`.
- Integration-tagged tests.
- Fuzz smoke runs for body and usage parsers.
- `govulncheck`.
- Confirmation that the build used the pinned toolchain rather than whichever one happened to be installed.
- Security static analysis.
- `CGO_ENABLED=0` build.
- Reproducible `-trimpath` release build with artifact checksums.
- Verification that the resulting Linux binary has no dynamic runtime dependency.
- Search proving no prompt/completion logging calls.
- Search proving no cost/currency implementation.
- Search proving no background upstream-probe loop.
- A black-box test that observes zero upstream requests while the binary is idle.
- A black-box test that `/v1/models`, startup, recovery, SQLite migration, and shutdown produce zero upstream requests.
- A schema inspection proving no body/header/cost columns were introduced.

Suppressions must name the exact linter and include a reason. Security-related warnings cannot be blanket-suppressed.

### 28.18 Performance and resource tests

Benchmarks and load checks must establish:

- Rewrite work is linear in body size.
- Memory is bounded by one request-body buffer, bounded segment metadata, the 8 MiB non-streaming precommit buffer, and small relay/observer buffers.
- A maximum-size body with a declared length allocates once, and all four attempts replay from the same segments.
- An unknown-length body charges every increase of its backing array before that increase happens, and its aggregate charge tracks capacity rather than length.
- SSE relay does not accumulate total stream size.
- Non-streaming bodies above 8 MiB do not accumulate total response size.
- Coordinator critical sections remain short.
- The admission commit’s own latency, measured on the filesystem the store will live on and at the concurrency the in-flight ceilings permit, reported against total dispatch latency. It is the only durable write on a request’s critical path, and it is what sizes the store-operation ceiling that bounds the pending-reservation window of §17.1.
- SQLite inserts do not hold coordinator state.
- Selection rechecks produce bounded aggregated skip state.
- 28 catalog entries and tens of thousands of rows do not materially affect startup.
- `/v1/models` requires no I/O beyond response writing.
- Idle process traffic to upstream is exactly zero.
- Sustained concurrency never violates account ceilings.
- Concurrent admitted handlers never exceed the global ceiling.
- Charged request, replay, and precommit memory never exceeds the aggregate budget.
- Streaming requests, requests waiting for an account, and requests backing off between attempts hold no precommit reservation.
- Thousands of small requests waiting on the gate create neither unbounded goroutines nor unbounded waiter metadata.
- A client that opens far more connections than it completes waits at accept instead of creating server goroutines, and both the connection and handler ceilings hold throughout.
- An unsized upload’s gate charge stays ahead of its buffered bytes at every step and settles to the actual size at read completion.
- Concurrent small unsized uploads cannot exhaust the aggregate budget, and a denied extension releases the whole charge and answers 429.
- A slow upload terminates at the request-read deadline instead of holding its buffer.

Benchmarks use `b.Loop`, run with `-benchmem`, and are compared across repeated runs with `benchstat` rather than by reading one number. No performance optimization is accepted if it weakens message preservation, rate correctness, or append-only evidence.

## 29. Tradeoffs and rationale

### 29.1 SQLite over JSONL

The argument that settles this is not durability, indexing, or the cost of the dependency. It is that this attempt log has a reader inside the proxy.

Session affinity survives a restart only if startup can answer "which account most recently served this session key, within the last hour", ranked by an arrival instant derived from two columns of the rows that come back, and it has to answer before the listener binds. JSONL is proposed on the premise that an attempt log is written and never read back, and that premise is false here: it would turn startup into a full scan of a file that grows for months, and would move the correctness of affinity recovery into hand-written parsing of a format that enforces nothing.

The rest is real but follows from the choice rather than driving it: typed nullable columns that keep a missing token count distinct from a zero, constraints that fail a malformed row at the write instead of at a read months later, crash-safe transactional commits, and safe concurrent reading by a local analysis tool while the proxy is serving.

The additional driver dependency and binary size are acceptable.

### 29.2 Manual dependency injection

The service has a small fixed dependency graph. Manual construction is explicit, testable, and adds no code generation, reflection, or lifecycle framework.

### 29.3 Small package structure

A single file would entangle JSON rewriting, HTTP commitment, rate state, and persistence. A large layered architecture would add indirection without additional domains. Cohesive internal packages provide the useful middle ground.

Splitting relay out of `internal/proxy` into a package of its own, on the prediction that `internal/proxy` becomes the largest and highest-risk package here, is considered and not taken. `internal/rewrite` earned its extraction on a property relay does not share: it has no HTTP dependency at all, its contract is a byte-level guarantee that stands as a public API in its own right, and its fuzz targets build without any server plumbing. Relay is HTTP from end to end, and the coupling the split would leave behind is the worst one in this design to turn into a cross-package contract, because the rule that no retry follows commitment is held jointly by the retry loop and the commitment state machine and owned by neither. A seam is extracted from code that exists and has shown where it wants to divide, not designed against a prediction about code that does not, and the file-size guideline is what forces the question at the point where it can be answered.

### 29.4 Bounded in-memory request buffering

Retries require a replayable request. Disk spooling would store prompt text, while unbounded buffering risks process exhaustion. A 64 MiB in-memory ceiling makes the resource cost explicit.

Consequences:

- A request larger than 64 MiB may be accepted by upstream but is rejected by this transport envelope.
- The body exists once. Route-owned replacements are small immutable segments over it, so replay costs metadata rather than a second copy, and the untouched bytes are never copied at all, which is the byte-preservation contract expressed as an allocation.
- Exactly one allocation is what a declared length buys, and the claim is worth stating narrowly rather than generally. A chunked body cannot be read into a single exact buffer without knowing its size first, so it grows geometrically inside a charge raised before each growth. The accounting follows capacity rather than length so that the slack a doubling leaves is never budget the process believes it still has.
- References to the buffered body should be dropped once no retry is possible.
- A per-request ceiling bounds one request only. Concurrency is what turns it into a process-wide number, so the aggregate gate is what actually bounds the resource, and the per-request limit is what makes each request’s share explicit.

### 29.5 Synchronous admission and terminal inserts

Synchronous writes make completion visibility deterministic and avoid an unbounded queue or background writer. Grouping a phase’s skips and terminal record in one transaction avoids one commit per recheck while retaining append-only rows. The accepted costs are that retry progression can wait for an earlier attempt’s transaction and that final streaming bytes may reach the client before final persistence.

The admission write is synchronous for a different reason: it is not analytics, it is the evidence required before a dispatch may leave this process, and evidence written after the send it was supposed to precede is evidence of nothing. Its costs are stated plainly because they are real. Every dispatch now pays one durable commit before the send, all such commits serialize through the single writer connection, and a store that cannot accept them makes the proxy refuse to originate traffic rather than serve it unrecorded. Against an upstream call measured in seconds, one commit is not the expensive part of a dispatch. The number missing from that sentence is how long the commit actually takes on the filesystem this store will live on, at the concurrency the in-flight ceilings permit, where each commit also queues behind every other statement on the connection. §28.18 measures it, and §15.2 states what that number does and does not decide.

### 29.6 Admission rows plus terminal attempt rows

Two immutable rows per dispatch satisfy append-only logging without updates and separate what must be true before the send from what can only be known after it. The remaining cost is that a hard crash still loses result metadata for an attempt in flight. What it can no longer lose is the evidence that the attempt was authorized. An admission with no terminal row is not a gap in the log; it is the durable statement that an attempt was authorized and that this process never recorded an ending for it, which is as far as the evidence goes and as far as §15.3 takes it. It is also what makes the ambiguous send of §21.7 countable, since every call that may have reached upstream has a row of its own whether or not the process survived to describe it.

### 29.7 Exact rolling window

It matches the configured account ceiling without boundary bursts. A deque is more exact than a token bucket and trivial at 60 timestamps per account.

Exactness still matters after the ceilings were raised, and arguably matters more. The ceiling is now a bound whose correct value is unknown and will be re-derived from the attempt log, and a limiter that admits boundary bursts would make that log measure the limiter’s own imprecision rather than upstream’s behavior. That is also why the window is anchored at the `Do` invocation rather than at the reservation that precedes it: an anchor that drifts by the store’s commit latency would fold this machine’s filesystem into a measurement of upstream’s tolerance.

### 29.8 One coordinator mutex

Account admission and new-session pin creation must be atomic. With three accounts, a single short critical section is simpler and safer than several locks and lock-order rules.

### 29.9 No strict waiter FIFO

A channel notification and recheck design is cancellation-friendly and simple. Strict FIFO would require considerably more queue state. Shuffle, a 60-second acquisition ceiling, and the logical request deadline limit starvation, but do not mathematically eliminate it.

### 29.10 Five-second stall before spill

Five seconds is the maximum wait for an unknowable in-flight release. RPM and cooldown waits use their exact known reopening time and skip the grace entirely when reopening falls outside it. This preserves cache across short overlaps without charging every spill a mandatory five-second delay.

### 29.11 Re-pin only on complete success

The alternative account has the most recent complete prefix only after a successful response. Failed or partial output is not a safe continuation anchor.

The same reasoning applies to the pin a first request creates. It has to exist before the response is known, because concurrent first requests must not each pick a different account, but it is held rather than owned: it survives while a request is still using it and disappears when the last one fails without confirming it. Leaving it to expire on the ordinary hour would take a session whose only request failed and make the account that failed it sticky for an hour, retrying the same failure on every turn.

### 29.12 Keyed session identifiers

Affinity is an equality test, so it needs a stable key and nothing else. A keyed digest keeps every routing decision and every restart recovery exactly as it was, bounds a column whose input is arbitrary client text, and removes the one place where content the proxy does not understand entered a durable store. The key is separate from the proxy bearer key because the two rotate for different reasons, and tying them would make a routine credential rotation drop every live conversation’s affinity.

### 29.13 Stop retries after commitment

This can expose a transport-level truncated response rather than transparently recovering, but retrying would create invalid concatenated output or duplicate tokens. Aborting rather than returning cleanly ensures clients can distinguish incomplete transport from a successful completion. Protocol correctness takes precedence.

### 29.14 In-memory health state

Credential disablement and cooldown are process-local, and restart is the entire recovery mechanism for both a rotated key and a renewed subscription on an unchanged key. A faster path was considered and rejected: letting an explicit account alias re-test a disabled account would buy back a restart that already costs one command on this machine, and would charge for it the only exception to the rule that a disabled account stays disabled, placed inside the admission path. Session affinity is recovered because it has direct cache value; rate correctness is carried across the restart by the blackout of §16.3 rather than by recovering anything.

### 29.15 Stable model list despite health

The model picker represents configured capabilities. Removing aliases based on transient health would make client behavior unstable and turn `/v1/models` into an implicit probe/readiness API.

### 29.16 No observability endpoint

SQLite and structured stderr cover the stated operational need. A metrics/debug endpoint would add an unsupported user-visible surface.

### 29.17 Bounded response observation

Small non-streaming bodies are buffered up to 8 MiB to make body-read failures retryable before commitment and make usage extraction reliable. Larger bodies and SSE remain incremental, and so does a body whose precommit allowance the aggregate gate declines, which loses that retryability rather than the response. The tradeoff is a bounded per-request memory allocation and delayed header/body delivery for non-streaming calls, whose clients ordinarily cannot use the completion until EOF anyway.

### 29.18 Request-header allowlist

A narrow allowlist may omit an exotic client header that a generic reverse proxy would forward. The fixed consumers do not require arbitrary header tunneling, and preventing accidental cookie, trace, forwarding, or machine-metadata leakage is more valuable.

### 29.19 Sixty-second account-acquisition ceiling

Waiting for all capacity until the full ten-minute logical deadline would convert local saturation into apparent service failure. Sixty seconds covers a complete RPM window while bounding interactive delay. Returning 429 with `Retry-After` gives the caller an accurate, retryable result.

### 29.20 Precommit streaming primer

Delaying downstream headers until one upstream body chunk is successfully read does not delay the first visible token, because the client could not consume a body before that chunk existed, and it preserves the ability to retry an upstream that closes immediately after headers.

### 29.21 Ambiguous-send duplication

Retrying a connection that failed after send may duplicate upstream generation. The proxy accepts duplicate flat-rate computation because it executes no tool side effects itself and because every dispatch remains independently limited and logged.

### 29.22 Same-account first retry for isolated server failures

An isolated 5xx/408/transient connection failure does not prove the account is unusable. Preferring it once preserves prompt cache; a repeated failure then prefers another account. Rate limits and upstream 504 responses still move away immediately because they are stronger account-specific signals.

## 30. Operations, cutover, and rollback

### 30.1 Required operational artifacts

Implementation is not complete without:

- A README containing installation, configuration, service lifecycle, log inspection, backup, recovery, cutover, and rollback procedures.
- A placeholder-only environment-file template.
- A reference user-service definition that runs the static binary as the current user, restarts only on process failure, and does not contain health probes.

- A `llmux version` subcommand printing release version, revision, the highest schema version the binary supports, and the Go toolchain, without opening configuration or a database.
- A `llmux db check` subcommand that opens a named database read-only and reports its schema version, `quick_check`, `foreign_key_check`, WAL state, file size, and the free space on its filesystem.
- A `llmux db backup` subcommand that writes an owner-only consistent backup, refuses to overwrite an existing file, and needs no account credential.
- A directory of named SQL query recipes covering the questions of §30.3, each executed in CI against a seeded store, which is what keeps a recipe and the schema it reads from drifting apart.
- Release artifacts built with `-trimpath`, published with checksums.

These are documentation and launch aids, not a UI or additional runtime service. The list still stops where a document would start describing the process rather than the system: a written checklist restating build identity would be a second copy free to be wrong, which is exactly why `llmux version` reads `debug.ReadBuildInfo` at runtime instead of printing strings someone stamped in by hand. It derives the answer rather than repeating it, and during a cutover or a rollback "which binary is this" is a question worth being able to ask the binary.

Build information cannot answer which schema version a database is at, because that is a property of a file and not of a binary. `llmux version` therefore reports the highest version its embedded migration set defines, which needs no configuration and no store to be present, and `PRAGMA user_version` belongs to `llmux db check`, which is handed a path. Splitting them is what keeps `version` answerable during a rollback, when the store may be the thing in question.

The two database subcommands exist so that §30.4 needs no second SQLite tool on the machine. Copying the main file while a WAL is active is not a backup, that section already says so, and the mechanism that is correct is the one this binary embeds. Neither subcommand opens a network connection, reads an account credential, or writes to the active store, and neither is an admin surface: they are offline commands an operator runs against a path they name.

### 30.2 Installation

The runbook must instruct the operator to:

1. Place the static binary in an owner-controlled executable location.
2. Create the SQLite parent directory before first launch.
3. Create the owner-only environment file outside the repository.
4. Set the five secrets and the absolute SQLite path.
5. Verify that the three account credentials are distinct without printing them.
6. Start the process and confirm that it binds only the expected address.
7. Call authenticated `/v1/models`.
8. Confirm that startup and the models call produced no upstream traffic.

The binary must never create a missing parent directory implicitly; a path typo must fail rather than create a new unintended log location.

### 30.3 Routine log inspection

The query recipes for the questions below are checked-in SQL files, each one executed in CI against a seeded store, and the README describes each by name and points at its file rather than carrying a copy of its text. A recipe that lives only in prose is schema knowledge that nothing fails for when the schema moves, which is the failure §1 spends a paragraph on; a recipe that is executed is tested the way the migrations it reads are, and a migration that invalidates one breaks the build rather than an operator.

Embedding them in the binary as a report subcommand was the alternative and is not taken. It would fix the same drift, but half of these questions are parameterized by an account or a time range, so the subcommand would either hard-code the answers, which makes it useless, or grow a query surface, which is the log-query API §3 rules out wearing a command-line hat. The two `db` subcommands that do exist are there because §30.4 needs a correct backup and a schema check that no other tool on the machine can produce; running a fixed SELECT is not in that class.

The logical-request recipes carry their weight: they are the questions a summary table would have answered, and having them written and tested once is what makes that table unnecessary rather than merely absent.

- Dispatch count by account and `event_at_us` range, which is the interval each call fell in rather than the interval its reservation did.
- Current/recent RPM pressure by account, read over that same column, so the operational number and the limiter’s own ceiling are measured at one boundary.
- Admission pressure by account and `reserved_at_us` range, and the admissions no terminal row ever matched. That second figure is the upper bound the window between the commit and the call leaves behind, and it is the only thing that separates a dispatch this store never described from one it never authorized.
- Upstream 429 responses against dispatch volume per account, together with the distribution of the retry delays advertised on those 429 rows, which is the one measurement that can show the local ceiling sits above upstream’s and by roughly how much upstream wants it lowered. The same column on a 5xx row is upstream describing an outage rather than a rate ceiling, so a recipe that does not filter by status mixes two different statements into one distribution.
- In-flight and RPM selection skips by account.
- Spill pivots with source and destination.
- Retry chains grouped by logical request, and the dispatch amplification they represent.
- Authentication failures and the account disablement they caused.
- The lookup of any `X-LLMux-Request-ID` a consumer reports, which resolves in `attempt_log` for a request that reached account selection and in `unrouted_request` for one rejected before it, and in exactly one of the two.
- Local rejections by error code and time range, which is what a consumer suddenly sending bodies the envelope refuses looks like from here.
- Client-visible outcome per logical request, which is the terminal row of each `logical_request_id` and not the union of its attempts.
- Final-response token counts per logical request, taken from that terminal row. Summing token columns across attempt rows counts a retried request several times, which is the one arithmetic mistake this schema invites.
- Attempt and logical-request latency distributions, the second being handler start through the terminal row rather than the sum of attempt durations.
- First-data-event distributions for streaming calls.
- Prompt, completion, and total token sums with nulls kept distinct from zeros. Where counts are absent, `usage_observation` says which reason applies instead of leaving it to be inferred, and a run of `unsupported_encoding` is a consumer that started advertising an encoding the bounded observer cannot decode.
- Session continuity and pin moves.
- Terminal capacity failures and their advertised retry time.
- Process uptime spans, read from `process_elapsed_us` on the stop row, and the runs no stop row closes, which are the start rows no stop row shares an instance identity with and which bound the unclean deaths from above rather than counting them, by §15.3. Neither is a difference between two wall stamps. This is what turns a missing `eod` row into either a consumer failure or proxy downtime.
- Requests whose headers were removed by the allowlist, which is how a consumer that started sending something new becomes visible.
- Per-consumer traffic, attributed from the presence of a `session_key`, the requested alias, and the streaming flag, because the store holds no consumer identifier. `eod` is the one sessionless caller, which is what makes its rows findable without appealing to the hour it usually runs at. This is a heuristic, and §15.5 records what would replace it and when.

No query recipe, subcommand, or report computes currency cost.

### 30.4 Database backup and archival

- There is no automatic rotation or retention.
- For a live backup, use `llmux db backup`, which drives SQLite’s own consistent backup mechanism rather than copying only the main file while WAL is active.
- Monitor the size of both the main database and its WAL, and the free space on the filesystem holding them.
- The runbook estimates monthly growth at the expected and at the maximum dispatch rate, so the size of this file is thought about before the day it fills a disk.
- Close long-lived external read transactions promptly, since an open one is what stops a checkpoint from completing.
- For a cold backup, stop the service cleanly, verify shutdown completed, then copy the database.
- Preserve schema version with every archive.
- Never delete or truncate the durable tables as part of normal service startup.
- Treat an admission-store failure or disk exhaustion as dispatch-blocking rather than as optional telemetry loss, because that is what the process does with it.
- Test restore by opening a copied database with a compatible binary and running read-only integrity/recovery queries.
- A backup or archive operation must not make any upstream request.
- Both reference commands are exercised by integration tests, because a restore procedure nobody has run is a hope.

Retention is out of scope and unbounded growth is not a plan, so there is one manual escape hatch. Nothing deletes rows, and the admission rule of §15.11 and §12 step 18 makes a full filesystem a dispatch outage rather than a logging inconvenience, so a store left alone eventually stops the proxy. Cold archive rotation is how an operator gets ahead of that:

1. Stop the service cleanly and confirm its stop row was written.
2. Take an owner-only archive with `llmux db backup` and verify it with `llmux db check`.
3. Preserve the archive and its checksum. Rotation never deletes one, which is what keeps the append-only rule true across the boundary.
4. Move the old database aside and start the service, which migrates a fresh one.
5. Accept that session affinity starts empty. At most the last hour of pins is lost, and rate safety needs no step of its own here: the restarted process refuses dispatch for a full rolling window whatever store it opens, which is the case this procedure used to cover by waiting before the archive was taken.

The procedure is manual and offline for the same reason retention is absent: anything that ran on its own would eventually run while the proxy was serving, and losing the evidence of a dispatch that happened is the one thing this store may not do.

### 30.5 Disabled-account recovery

An upstream 401 disables an account for the rest of the process lifetime, and nothing inside the process puts it back:

1. Confirm from the attempt log which account was disabled and when.
2. If the credential itself changed, update the owner-only environment file.
3. Restart the service.
4. Confirm a later flexible request can use the account.

Restart is the whole procedure, for a rotated key and for a renewed subscription on an unchanged key alike. Do not add reload or probe machinery.

### 30.6 Pre-cutover acceptance against real upstream

These checks are deliberately manual because they consume real account quota:

1. Call `/v1/models` and confirm all 28 exact aliases.
2. Confirm that every provisional model ID was replaced and that every reasoning preset, `max` above all, passed the Phase 0 gate.
3. Make one small exact-account request through each of `k1`, `k2`, and `k3`.
4. Run a multi-turn `pi` conversation containing at least one complete tool-call/tool-result loop.
5. Confirm every turn’s raw `messages` value arrives upstream unchanged using the approved diagnostic fixture, not production prompt logging.
6. Run one representative `kernl` non-streaming job.
7. Run one `eod` dry run without a session file.
8. Exercise one controlled spill scenario.
9. Exercise one retryable error against a test upstream, not by intentionally wasting real upstream requests.
10. Inspect SQLite for account, spill, retry, skip, latency, first-event, and token facts.
11. Run the privacy-marker inspection against the database, WAL, and process logs.

Record pass/fail and timestamps for each check without copying prompts, completions, keys, or raw upstream error bodies. The durable acceptance evidence is the attempt store itself, which holds one row per upstream attempt from the first real request onward; a parallel handwritten record competes with it and loses.

### 30.7 Consumer preconditions

Bounding account acquisition at 60 seconds introduces a result the replaced proxy never produced: a local 429 with `Retry-After`, meaning this proxy declined to keep queueing, not that upstream refused the request. A caller that does not retry a 429 turns local saturation into a lost result.

That is a change in the callers, and it stays there. The proxy holds no model of who calls it and gains none; it answers with a retry delay and the caller decides what to do with it. Widening the wait inside the proxy to accommodate a caller that cannot retry would reintroduce the ten-minute hang the ceiling exists to remove.

Before cutover:

- Confirm each caller retries a local 429 and honours `Retry-After`.
- The once-a-day `eod` summariser is the one to change first. It runs with no session file and no operator watching, so a lost result there is invisible until someone notices a missing day. It needs retry in its own codebase before cutover.
- A caller that cannot be changed is recorded as accepting the loss, rather than answered by relaxing the ceiling here.

The token and latency evidence has preconditions of the same shape, and they exist for the same reason: the proxy will not alter a request or a response to improve its own observability, so what the callers send decides what the log can hold. Each of these is a caller that quietly writes null columns forever, and the absence is first noticed a month later in §30.3’s token recipes rather than at cutover.

- Confirm that each streaming caller which wants durable token evidence sends `stream_options.include_usage` itself. The proxy never injects it, by §4 invariant 18, so a streaming consumer that omits it reports no usage object and every token column stays `NULL`. This one matters most for `pi`, the primary streaming consumer.
- Confirm that a caller which wants durable token and first-event evidence advertises only response encodings the observer can decode, once Phase 0 has recorded which encodings upstream actually selects. A caller that asks for one the observer cannot decode receives its bytes exactly as ever and writes `NULL` observation columns, which is its choice to make and is recorded here rather than worked around inside the proxy.

### 30.8 Cutover

1. Preserve the existing proxy binary and configuration for rollback.
2. Stop the old listener cleanly.
3. Start `llmux` on `127.0.0.1:4000`.
4. Verify authenticated `/v1/models`.
5. Run one small exact-account request, allowing for the post-start dispatch blackout, which can delay the first dispatch after any start by up to one rolling window.
6. Run the `pi` multi-turn tool-loop check.
7. Run representative `kernl` and `eod` checks.
8. Confirm all three consumers still use only the documented base URL, proxy key, model aliases, and session header, and that none of them appends a query string.
9. Inspect the first attempt rows before declaring cutover complete.

No traffic shadowing is used: duplicating real prompts to both proxies would spend quota, duplicate sensitive content in flight, and complicate comparison.

### 30.9 Rollback

Rollback must be reversible and must preserve evidence:

1. Stop `llmux` cleanly.
2. Preserve its SQLite file and process logs; do not delete or rewrite them.
3. Restart the previous proxy on port 4000.
4. Confirm the previous proxy’s normal liveness behavior.
5. Record the rollback reason and the last `llmux` logical request timestamp.

The old proxy does not read or migrate the `llmux` database. A rollback therefore cannot corrupt the new attempt log.

### 30.10 Post-cutover observation

For the first week of real traffic, inspect the attempt store at least daily for:

- Any account exceeding the designed local ceilings.
- Upstream 429 rate against dispatch rate per account. Re-derive the per-account dispatch and in-flight ceilings, and the cooldown threshold, from it once a week of real traffic exists, remembering that the absence of 429s bounds the ceiling from neither side. Where §14.4 shipped, the lowest remaining quota observed across the week bounds it from below, which is the direction 429 silence never could, and the re-derivation stops being one-sided. The retry delays stored on the 429 rows are upstream’s own quantitative statement of how far over the line a burst landed, and they are the direct input to the cooldown constants; the same column on a 5xx row belongs to a different question and is filtered out here.
- Repeated authentication failures.
- Spill frequency and pin-move correctness.
- Retries after response commitment, which must remain zero.
- Suppressed retries by `retry_disposition`, which separates a failure upstream declared final from one this proxy would have retried and could not, and says whether the ten-minute deadline or a dispatch budget is the binding constraint on retry.
- Response truncations or client disconnects.
- Attempt-log write failures.
- Missing `eod` execution evidence in its expected window.
- Prompt/completion/key marker leakage, which must remain zero.

This is human log review, not a background health checker. Any discovered defect becomes a reproducible automated test before correction.

The ceiling re-derivation above is the one obligation this document carries that falls after the last phase of §31, which is why its gate is stated here rather than there. It is owned by whoever runs this review, and it is complete when a dated entry records one of exactly two outcomes for the two per-account ceilings of §9.2 and for the cooldown threshold and duration of §20.2: new values, written into those sections, or the existing values kept, with the week’s dispatch, 429, saturation and latency figures that support keeping them. Neither outcome is the absence of an entry. A constant that no review has either changed or confirmed is still the guess §9.2 admits it to be, while the evidence that would settle it accumulates in a store nothing deletes.

## 31. Implementation sequence

### Phase 0: Contract inventory

Deliver:

- Exact upstream model strings copied from the current deployment.
- Real-upstream pass/fail evidence for every distinct model and preset, per account.
- A settled answer for `reasoning_effort="max"`: supported and distinct, or replaced, or removed. Record what the compatibility page says on the day as context, never as the answer.
- The status upstream returns for an invalid or revoked key, recorded from a deliberately bad credential.
- Which `Content-Encoding` upstream selects when sent each consumer’s real `Accept-Encoding`, recorded per encoding advertised and separately for a streaming and a non-streaming request. This decides whether the bounded observation decoder of §14.3 ships in Phase 6 and which encodings the §30.7 consumer precondition has to name.
- Whether upstream responses carry numeric rate-limit headers, recorded by exact header name and per account. This decides whether the bounded projection of §14.4 ships in Phase 5, and it costs one look at headers the inventory requests are already receiving.
- From the same look: which form a real `Retry-After` takes, and whether a `Date` header accompanies it. This gates nothing, because §20.2 has to handle both forms whatever upstream prefers, and it is recorded here because it says in advance which path a production 429 will take and whether the absolute form is derivable at all on this provider.

Gate:

- No provisional catalog value remains in this document.
- Every route is reachable on all three accounts declared eligible for it.
- An unsupported or indistinguishable preset is resolved here rather than in code.
- If a revoked key answers 403 rather than 401, §20.1 needs its documented credential-specific code before Phase 5, or account disablement never fires.

This phase writes no code and produces no binary. It exists because everything after it is built on strings that are currently guesses.

### Phase 1: Project skeleton and fixed catalog

Deliver:

- Go module carrying the Go 1.26 language version and a toolchain directive naming the exact current 1.26 patch release, with CI building on that toolchain and no other.
- Minimal command and application composition.
- Configuration loading/validation.
- Fixed route catalog.
- Generated account variants.
- Deterministic `/v1/models` projection.
- Placeholder-only environment template and reference user-service definition.

Gate:

- Catalog and configuration unit tests pass.
- No network or database behavior yet.
- The module’s toolchain directive names one exact 1.26 patch release, and CI builds on that toolchain and no other. This phase owns the choice §5 declines to carry, and §28.17’s confirmation that a build used the pinned toolchain runs from here onward rather than at the first release, because a toolchain nobody pinned is whichever one the machine happened to have.

### Phase 2: SQLite durable store

Deliver:

- Pinned cgo-free driver.
- Embedded initial migration.
- Append-only `dispatch_admission`, `attempt_log`, `process_event`, and `unrouted_request` tables with their triggers.
- Secure file pre-creation and permission checks.
- Synchronous fail-closed admission insert API.
- Transactional phase-batch insert API.
- Dispatch, selection-skip, and selection-failure records.
- The session recovery query.
- Permission and schema checks.

Gate:

- Empty/upgrade/concurrency/atomic-batch/append-only tests pass.
- An admission insert that fails is reported to its caller as a failure and never as a partial success.
- The admission commit’s latency is measured on the deployment filesystem and recorded, so the store-operation ceiling that bounds the pending-reservation window of §17.1 is a number rather than a guess, and the one durable write on the dispatch critical path has a measured cost before anything is built on top of it.
- Connection-local pragmas are confirmed to hold on every connection the pool hands out, on the pinned driver, by reading them back after the pool has been forced to open a fresh one. A pragma is per connection and a pool may open another at any moment, so setting it once at open is an assumption about this driver and this pool rather than a property of SQLite.
- Static cgo-free test build succeeds.

### Phase 3: Request scanner and rewriter

Deliver:

- Bounded body reader.
- Top-level lexical scanner with a bounded depth.
- Model resolution.
- Fixed top-level injection.
- Immutable segmented replay plan.
- Exact raw-message preservation.
- Global handler slots and the weighted memory gate.

Gate:

- Unit/property/fuzz tests pass.
- Replay across four attempts allocates one body-sized buffer.
- Representative multi-turn tool-loop fixtures remain byte-identical inside `messages`.

### Phase 4: Coordinator

Deliver:

- Account state.
- Exact rolling limiter.
- In-flight leases.
- Session pins.
- Notification waits.
- Shuffle selection.
- Reopen-aware bounded wait and spill behavior.
- Sixty-second acquisition ceiling and `Retry-After`.
- Health transitions.
- Post-start dispatch blackout.
- Startup session-pin loading.

Gate:

- Fake-clock tests pass.
- Coordinator and fake-upstream stress tests prove account ceilings at the true dispatch boundary.
- No goroutine leaks.

### Phase 5: Upstream execution and retry

Deliver:

- Shared HTTP transport with explicit proxy, TLS, protocol, connection-pool, and response-header limits.
- Fixed request-header allowlist, response hop-by-hop filtering, and the response state/routing strip list.
- Empty-query enforcement and rejection of an unexpected redirect or upgrade.
- Attempt classification.
- Retry budgets/backoff.
- Class-specific account-choice behavior.
- Intermediate response drain.
- Ambiguous-send accounting.
- The bounded rate-limit header projection of §14.4, if Phase 0 recorded upstream sending one, together with the migration that adds the two columns it fills. Both are this phase’s, in that order; neither is part of the initial migration Phase 2 delivers.

Gate:

- Scripted upstream matrix passes.
- Each dispatch maps to one record.
- Either the rate-limit columns exist in §15.5, carry their constraint in §15.8, and are read by a §30.3 recipe that §28.13 executes, or Phase 0 recorded no such header family and none of those three places changed. A column that ships in a migration and in none of the three is the drift §0 rule 8 exists to prevent.

### Phase 6: Relay and usage observation

Deliver:

- Streaming and non-streaming relay.
- Streaming first-read primer.
- 8 MiB non-streaming precommit buffer and progressive fallback.
- SSE flushing.
- Client cancellation.
- Final response commitment rules.
- Committed-response abort handling.
- First-data-event observation.
- Selective token extraction.
- Bounded observation-side gzip decoding, if Phase 0 recorded upstream selecting a compressed encoding.
- Exact final response preservation.

Gate:

- Byte-for-byte response tests pass.
- No retry occurs after commitment.
- Precommit failures retry and post-commit failures abort.
- Large/slow/truncated streams remain bounded.
- Privacy-marker tests pass.

### Phase 7: Lifecycle and hardening

Deliver:

- Signal handling.
- Graceful and forced shutdown.
- Panic recovery.
- Structured lifecycle logging.
- Durable process start and stop rows.
- Secure file checks and the single-instance store lock.
- Static build automation with `-trimpath` and published checksums.
- `llmux version`, deriving its output rather than restating it, and the read-only `llmux db check` and `llmux db backup`.
- Lint/security configuration.

Gate:

- Shutdown/restart/failure-injection tests pass.
- Crash-boundary admission tests pass.
- Aggregate-memory and slow-client tests pass.
- No background upstream request occurs while idle.

### Phase 8: Operations and black-box acceptance

Deliver:

- Installation and environment documentation.
- The named SQLite query recipes and their CI execution, plus the backup, restore, and archive runbooks.
- Disabled-account recovery procedure.
- Cutover and rollback procedures.
- Real-binary socket/signal/flush smoke test.

Gate:

- The compiled binary passes black-box lifecycle tests.
- Startup, models listing, idle operation, recovery, and shutdown make zero unsolicited upstream requests.
- Rollback can be completed without modifying the SQLite store.

### Phase 9: Consumer acceptance

Run representative requests for:

- `pi` streaming with session affinity.
- Multi-turn tool loop.
- `kernl` non-streaming calls.
- `eod` no-session call.
- Base aliases.
- All account-pinned variants.
- Saturation and spill.
- Retryable and non-retryable failures.

Inspect SQLite to prove:

- Serving account is visible.
- Spills are visible.
- Retries and skip reasons are reconstructable.
- Terminal capacity failures are explicit.
- Session pin moves are reconstructable from spill source and serving account.
- Latency and first-event timing are present where observable.
- Token counts are present only when upstream reports them.
- No prompt/completion text or cost exists.

## 32. Definition of done

The project is complete only when all of the following are true:

- One static cgo-free binary starts with the required five secrets and the SQLite path.
- It listens on the configured loopback address and refuses any other.
- A second process pointed at the same store refuses to start while the first is alive, so the ceilings are enforced by one counter set and not by two.
- Both endpoints enforce the shared bearer key by digest comparison, and startup rejects a key that is too short or shared with an account.
- `/v1/models` lists exactly 28 deterministic aliases without upstream I/O.
- Every alias resolves to its fixed upstream model and eligible account set.
- The raw `messages` value is byte-identical at upstream.
- Every untouched top-level field retains its raw bytes and relative order.
- Request replay uses one body buffer plus bounded segment metadata, exact when the length was declared, and reuses it across every attempt.
- Aggregate admitted-request memory stays within the configured budget.
- Unsupported parameters are forwarded.
- The two reasoning presets are injected only at top level.
- No usage-requesting field is injected.
- Streaming and non-streaming final responses preserve status, headers, and body bytes.
- Streaming responses remain retryable until the first upstream body byte is committed.
- Non-streaming responses remain retryable through the bounded precommit phase.
- Post-commit upstream read failures abort rather than ending as clean responses.
- Session affinity remains account-wide for one sliding wall-clock hour, and a pin recovered at startup expires at the instant a live one would.
- Saturated pins wait only when reopening can plausibly occur within five seconds, then spill when possible.
- Every account-acquisition phase ends within 60 seconds.
- Temporary local capacity exhaustion returns 429 with `Retry-After`.
- Successful spill updates affinity; failed/partial spill does not.
- No account exceeds 60 dispatch starts in a rolling minute, measured at the `http.Client.Do` boundary rather than at reservation.
- No account exceeds twelve in-flight attempts.
- Those ceilings are proven from the fake upstream’s observations, not only coordinator counters.
- Every actual dispatch has a durable admission row committed before it.
- No dispatch leaves during the first full rolling window after any process start, proven across crash-restart boundaries with the wall clock stepped in both directions.
- Aliases and pinned variants cannot multiply an account’s capacity.
- Retry behavior matches the classification table.
- No retry occurs after downstream commitment.
- Upstream 401 disables an account on its first failure, nothing but restart re-enables it, and the upstream 401 itself never reaches a client.
- Upstream 403 alone never disables an account.
- A `Retry-After` blocks only the account that sent it.
- Repeated 429 responses cause bounded cooldown.
- No background health or model probes exist.
- Each dispatch has exactly one append-only terminal row when its transaction succeeds.
- Local limiter/health skips are durably visible.
- A no-dispatch capacity failure has an explicit terminal record.
- All record IDs are proxy-generated.
- Clients receive `X-LLMux-Request-ID` and can find their own request in SQLite with it, including a request the envelope rejected before routing, and it resolves in exactly one table.
- Prompt and completion text are absent from durable and process logs.
- Raw session identifiers are absent from both.
- Currency and cost logic are absent.
- Startup recovers successful session pins and reads no rate state.
- Every process start, and every shutdown the process survives to perform, is a row in the durable store.
- Graceful shutdown releases permits and closes SQLite last.
- Request bodies, account acquisition, retries, downstream writes, and store cleanup each have an independently enforceable bound.
- Installation, backup, recovery, cutover, and rollback runbooks are complete and tested.
- Unbounded store growth has a tested offline archive procedure rather than an eventual dispatch outage, and it deletes nothing.
- Real Ollama Cloud acceptance includes a multi-turn tool-call/tool-result loop.
- Unit, integration, race, fuzz, model-based, crash-boundary, privacy, resource, failure-injection, restart, and static-build gates pass.
