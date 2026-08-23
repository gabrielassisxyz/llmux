#!/usr/bin/env bash
# tests/bin-ci_test.sh
#
# Unit tests over bin/ci's source-detection, skip-classification and exit-status logic.
# The defect this guards against is bin/ci reporting a green run after skipping every Go
# check: a tree whose index tracks no .go paths while .go sources sit on disk is exactly
# the state where a green banner is a claim about zero work.
#
# Each case runs the real bin/ci script inside a sandbox directory with a real git index
# (git init), so the index state is real rather than stubbed. The sandboxes carry a
# minimal Go module that is enough to make every Go check pass when sources are tracked,
# so the harness-only and sources-on-disk cases are the only difference between runs.
#
# Run with:
#
#   tests/bin-ci_test.sh
#
# Exit status is non-zero when any case fails.

set -uo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$TEST_DIR/.." && pwd)"

GO_VERSION="$(awk '$1 == "go" { print $2; exit }' "$REPO_ROOT/go.mod")"
TOOLCHAIN="$(awk '$1 == "toolchain" { print $2; exit }' "$REPO_ROOT/go.mod")"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail_count=0

fail() { printf 'FAIL: %s\n' "$1"; fail_count=$((fail_count + 1)); }
pass() { printf 'ok:   %s\n' "$1"; }

# Symlink a controlled subset of the host's tools into one directory, used to simulate a
# missing optional tool. Builtins (echo, cd) resolve to a bare name and are skipped.
make_tools_dir() {  # $1 = dir; remaining args = tool names to expose
    local d="$1"; shift
    mkdir -p "$d"
    local t p
    for t in "$@"; do
        p="$(command -v "$t" 2>/dev/null || true)"
        case "$p" in
            /*) ln -s "$p" "$d/$t" ;;
        esac
    done
}

write_go_mod() {  # $1 = sandbox dir
    printf 'module sandbox\n\ngo %s\n\ntoolchain %s\n' "$GO_VERSION" "$TOOLCHAIN" > "$1/go.mod"
}

# A minimal module that passes every Go gate bin/ci runs: a main package plus the two
# fuzz targets the fuzz smoke run names. No third-party imports, so vet, lint, the
# dependency gate and govulncheck all have nothing to trip on.
write_go_sources() {  # $1 = sandbox dir
    local d="$1"
    mkdir -p "$d/internal/rewrite"
    printf 'package main\n\nfunc main() {}\n' > "$d/main.go"
    {
        printf 'package rewrite\n\nimport "testing"\n\n'
        printf 'func FuzzScan(f *testing.F) {\n\tf.Add("")\n\tf.Fuzz(func(t *testing.T, s string) {})\n}\n\n'
        printf 'func FuzzApplyInjection(f *testing.F) {\n\tf.Add("")\n\tf.Fuzz(func(t *testing.T, s string) {})\n}\n'
    } > "$d/internal/rewrite/fuzz_test.go"
}

# make_sandbox <name>, a harness-only checkout: bin/ci, its two support scripts and a
# go.mod, tracked in a fresh git repo. No .go files anywhere.
make_sandbox() {
    local d="$WORK/$1"
    mkdir -p "$d/bin" "$d/scripts"
    cp "$REPO_ROOT/bin/ci" "$d/bin/ci"
    cp "$REPO_ROOT/bin/slop-guard" "$d/bin/slop-guard"
    cp "$REPO_ROOT/scripts/md-unwrap.py" "$d/scripts/md-unwrap.py"
    write_go_mod "$d"
    (
        cd "$d" || exit 99
        git init -q
        git config user.email ci@example.invalid
        git config user.name ci
        git add bin scripts go.mod
        git commit -qm init
    )
    printf '%s' "$d"
}

run_ci() {  # $1 = sandbox dir; $2 = optional PATH override
    local d="$1" path="${2:-$PATH}"
    (
        cd "$d" || exit 99
        PATH="$path" bash bin/ci >out.log 2>&1
        printf '%d' "$?" > exit.code
    )
}

check_exit() {        # expected actual label
    if [ "$1" -eq "$2" ]; then pass "$3"; else fail "$3 (expected exit $1, got $2)"; fi
}
check_contains() {    # needle haystack label
    if grep -qF -- "$1" "$2"; then pass "$3"; else fail "$3 (missing: $1)"; fi
}
check_matches() {     # regex haystack label
    if grep -qE -- "$1" "$2"; then pass "$3"; else fail "$3 (no match: $1)"; fi
}

# 1. Tracked Go sources pass and the green banner names how many checks ran.
d="$(make_sandbox tracks_go_sources)"
write_go_sources "$d"
(
    cd "$d" || exit 99
    git add main.go internal
    git commit -qm sources
)
run_ci "$d"
check_exit 0 "$(cat "$d/exit.code")" "tracked Go sources exit 0"
check_matches 'all [0-9]+ checks passed' "$d/out.log" "green banner names the check count"

# 2. Go sources on disk but none tracked fails and names the staging cause.
d="$(make_sandbox sources_on_disk_not_tracked)"
write_go_sources "$d"
run_ci "$d"
check_exit 1 "$(cat "$d/exit.code")" "sources on disk but untracked exit non-zero"
check_contains 'never staged' "$d/out.log" "failure names the staging cause"

# 3. A harness-only checkout with no .go files anywhere still passes.
d="$(make_sandbox harness_only)"
run_ci "$d"
check_exit 0 "$(cat "$d/exit.code")" "harness-only checkout exits 0"
check_contains 'harness-only' "$d/out.log" "harness-only banner is distinct"

# 4. A missing optional linter is skipped, reported, and still exits 0. golangci-lint is
# hidden by a PATH that keeps the Go toolchain and system tools but omits the linter.
d="$(make_sandbox missing_linter)"
write_go_sources "$d"
(
    cd "$d" || exit 99
    git add main.go internal
    git commit -qm sources
)
restricted="$(dirname "$(command -v go)"):/usr/bin:/bin"
run_ci "$d" "$restricted"
check_exit 0 "$(cat "$d/exit.code")" "missing optional linter exits 0"
check_contains 'optional tool not installed' "$d/out.log" "tool-missing skip is categorized"
check_contains 'golangci-lint' "$d/out.log" "golangci-lint is named in the skip"

# 5. The two skip kinds are counted separately. A harness-only checkout with gitleaks
# absent produces both a no-input skip and a tool-missing skip in one run.
d="$(make_sandbox summary_counts_separately)"
tools="$WORK/tools"
make_tools_dir "$tools" bash sh env dirname awk sed grep find mkdir cat mktemp rm git python3 go
run_ci "$d" "$tools"
check_exit 0 "$(cat "$d/exit.code")" "harness-only with gitleaks absent exits 0"
check_contains 'check(s) skipped: optional tool not installed' "$d/out.log" "tool skips counted separately"
check_contains 'check(s) skipped: nothing of this kind to check' "$d/out.log" "no-input skips counted separately"

if [ "$fail_count" -eq 0 ]; then
    printf '\nall bin/ci tests passed\n'
    exit 0
else
    printf '\n%d bin/ci test(s) failed\n' "$fail_count"
    exit 1
fi
