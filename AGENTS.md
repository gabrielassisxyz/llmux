# llmux Agent Briefing

> Read before every interaction. Living spec: short, imperative. On every gotcha or decision, append one line to *Common hurdles*.

> **What it is:** an OpenAI-compatible routing proxy. It fronts three Ollama Cloud account keys behind one local endpoint, resolves a short model alias to an upstream model plus an account, forwards the request unaltered, and records one row per upstream attempt.

> **Calibration:** Tier 2 · Phase: **work**. External stakes are contained (one operator, one machine, no tenancy), but personal stakes are real: it sits on the critical path of three consumers, and the defect it exists to fix was a proxy corrupting requests silently. Update the phase as the project moves `work → right → fast`.

> **Review gate:** `standard`. One independent opinion over the whole branch diff, once, pre-push. No per-commit or mid-development reviews.

## Stack & Commands

- **Stack:** Go 1.26, standard library first. `net/http` for both the server and the upstream calls. No web framework, no router dependency, no ORM.
- **Build:** `go build ./cmd/llmux`
- **Run:** `go run ./cmd/llmux`
- **Test:** `go test ./...` · with the race detector: `go test -race ./...`
- **All gates at once:** `bin/ci`, the exact thing CI runs, so green locally means green in CI.
- **After clone, once:** `bin/install-hooks` (points git at `.githooks/`).
- **Before your first write:** `bin/worktree new <type>/<kebab-desc>`.
- **Planned work:** `br` beads, triaged with `bv`. `br ready` is what to pick up next. The tracker state is **not committed to this repo**: `.beads` is a symlink into `local/`, so the graph is versioned with the maintainer notes instead. It records what is being considered, reordered and abandoned, which is a different document from a statement of direction, and `ROADMAP.md` is the part written for whoever arrives. Versioned but unpublished, because once a plan has been decomposed the graph *is* the planning output, and a graph held on one disk with no history is one failure away from having to be rebuilt from scratch.

## Scope (current)

**Current scope:** one OpenAI-shaped route over one upstream API shape, for the three consumers that speak to it today. Don't expand beyond it without a present need; if a change drifts past it, STOP and flag it.

What that rules out, stated because each one is a plausible next step that is not wanted: no second provider dialect, no translation layer, no tenancy, no user model, no web UI, no plugin surface, no runtime-configurable backend list. The route catalog is fixed in source. A router over one API shape is the entire design.

## Invariants

These are not preferences. Each one is a defect that was measured in the proxy this replaces, and re-introducing any of them recreates the bug that motivated the project. The full statement is `docs/plans/PLAN.md` §4 and §5.

- **Never touch the message array.** The conversation crosses byte for byte. The original failure was not a deletion but a one-way field rename with no inverse on the way back up, which made the model stop reasoning and stop converging. Any proxy that translates a field name owes the inverse translation; a router over one shape never incurs that debt. Route-owned parameter injection is the *only* permitted mutation.
- **No parameter validation.** Forward what the client sent. Do not adjudicate what the upstream supports; rejecting a parameter the upstream documents as valid is how the previous proxy broke working requests.
- **Count rate limits per account, never per deployment or per alias.** An account is the thing with a real ceiling. Per-deployment counters let one conversation reach the same account through two names and send twice the configured rate.
- **Never key anything on an id the upstream generated.** Upstream response ids repeat within a small space; using one as a primary key lost 83% of log rows.
- **No background health checks.** Probing every deployment on a timer was measured in tens of thousands of billed requests a day. Retries, failure counters and dropping a bad key on sight are what cover a dead account instead.
- **Log token counts, never prompts or completions.**
- **Never compute cost.** The upstream is flat-rate and never reports the cached share, so any figure would be a ceiling presented as a measurement. Log tokens; price downstream.

## Tests (TDD)

- Every feature is born with a test; every bugfix with a regression test.
- Tests run with ONE command, no manual setup, no real credential, no network.
- **Hermetic by default.** The clock, the permutation source, the upstream executor and the record writer are the injection boundaries; use a named fake, never an inline stub. A test that needs a real account key is misplaced.
- **`go test -race` is a hard gate from the first package, not a pre-release step.** Session pinning, per-account rolling windows and concurrent stream relay have no correctness story a single-threaded run can tell.
- Before saying "done", run `bin/ci` and report the result.

## Small releases

- Every commit on `main` passes `bin/ci` and is production-ready. No "broken commit I fix in the next one".
- Closed work is committed before switching tasks; flag it if it has not been.

## Security (habit, not a phase)

- The proxy holds four credentials: the client bearer key and three upstream account keys. They arrive from the environment, they are never logged, never written to the attempt store, and never echoed in an error.
- When touching request parsing, header forwarding, the listen address or the store, flag the risk and propose the guard.
- Dependency CVEs are caught by `govulncheck` in `bin/ci` and in CI.

## Prose

This repository is public. Everything below applies to Markdown, source comments, config values, commit messages and PR text alike, because those are the three places it leaked last time.

- No em-dash. Use a comma, a colon, a semicolon or a full stop. `bin/ci` checks this, with a per-line `allow-emdash` escape hatch for the rare case where one is genuinely content.
- Markdown is soft-wrapped: one paragraph, one line. `bin/ci` checks this too. Fix a failure with `python3 scripts/md-unwrap.py --write .` rather than by hand.
- Bold marks structure, such as a bullet lead-in or a table header, never emphasis in the middle of a sentence. Same for italics: a term being introduced, not a word being stressed.
- No process narration anywhere a stranger can read it. No task ids, no phase names, no review rounds, no mention of who or what reviewed a diff, no reference to a session or a conversation. Commit and PR text describe the problem and the change, never how the work was organised.
- No audience in the text. A README says what the software does, not who is going to read it or why.
- Comment density is low by default: the non-obvious only, the why and not the what. Long reasoning belongs in a document, not in a header comment.

## Git & secrets

- Before any commit, show `git status` and `git diff --cached`; confirm no secret is staged. If you spot one, STOP and report it. The gitleaks pre-commit hook is the deterministic backstop; this habit is the probabilistic one.
- Real secrets stay out of git. Only `.env.example`, with fake values, is committed.

## Post-implementation checklist (run before "done")

1. Commits small and well-described.
2. Refactoring candidates listed, if the change was large.
3. Security risks flagged, if you touched a sensitive surface.
4. This spec updated if behavior, setup or release flow changed, and any hurdle it gained is classified rather than just appended.

## Common hurdles

| hurdle | class | gate |
|---|---|---|
| A gitleaks test using AWS's documentation key (`AKIAIOSFODNN7EXAMPLE`) passes: gitleaks allowlists it. Probing the hook with one proves the gate works when it does not. Use a pattern that is not allowlisted. | prose | none, judgement |

**A hurdle promoted to a gate is deleted from this table, not duplicated.** The gate is the instruction; a line here restating it only dilutes the ones still unguarded.

Classify at the moment of writing, or the table becomes a graveyard nobody reads to the bottom of: **`ci`** means a named command catches it and it belongs in `bin/ci` (the row exists only until it is wired); **`tripwire`** means no check is possible but a predictable command precedes the mistake, so the knowledge has to appear at that command; **`prose`** means genuine judgement, no check and no trigger. If you cannot name the exact command, it is not `ci`.

---

The rules below are project-independent and identical across every repo that carries them. They are stamped from a single canonical source, so edit them there, never here.

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
