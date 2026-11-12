#!/usr/bin/env bash
# lib/selftest.sh — self-test of lib/assert.sh, run by run.sh before the scenarios.
#
# Pins the SIGPIPE trap (D214): under `set -o pipefail`, `echo "$body" | grep -q`
# dies of SIGPIPE when the body outlasts the pipe and the match is early, so a
# passing check reported "lacks" and a leak went undetected. The body here is
# 300 KB (a pipe holds 64 KB) with the needle on line 1, so the old helpers fail
# this test every time and the here-string ones never do. No server is needed.
#
# Exit code: 0 when every helper answered right, 1 otherwise.

set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=assert.sh
source "${SELF_DIR}/assert.sh"

NEEDLE='ALREADY-SENT-NEEDLE'
FILLER="$(head -c 300000 /dev/zero | tr '\0' 'x')"
WITH_NEEDLE="${NEEDLE}"$'\n'"${FILLER}"
WITH_ERROR='{"isError":true,"text":"forbidden: x"}'$'\n'"${FILLER}"
WITHOUT_NEEDLE="${FILLER}"

SELF_FAILURES=0

# expect <pass|fail> <label> <assert_fn> <args...>
#   Runs the helper, discards its [PASS]/[FAIL] line and the failure it counted,
#   and checks that it answered <pass|fail> as the body demands.
expect() {
    local want="$1" label="$2"; shift 2
    local before="$E2E_FAILURES" got
    "$@" >/dev/null
    if [[ "$E2E_FAILURES" -gt "$before" ]]; then got=fail; else got=pass; fi
    E2E_FAILURES="$before"
    if [[ "$got" == "$want" ]]; then
        echo "[selftest] ok   ${label} (${want})"
    else
        echo "[selftest] FAIL ${label}: want ${want}, got ${got}"
        SELF_FAILURES=$((SELF_FAILURES + 1))
    fi
}

expect pass 'assert_mcp_contains finds an early needle in a 300 KB body' \
    assert_mcp_contains d "$WITH_NEEDLE" "$NEEDLE"
expect fail 'assert_mcp_not_discloses sees an early needle in a 300 KB body' \
    assert_mcp_not_discloses d "$WITH_NEEDLE" "$NEEDLE"
expect fail 'assert_mcp_ok sees an early isError in a 300 KB body' \
    assert_mcp_ok d "$WITH_ERROR"
expect pass 'assert_mcp_error_text finds an early error in a 300 KB body' \
    assert_mcp_error_text d "$WITH_ERROR" forbidden
expect fail 'assert_mcp_contains lacks a needle that is not there' \
    assert_mcp_contains d "$WITHOUT_NEEDLE" "$NEEDLE"
expect pass 'assert_mcp_not_discloses passes a body without the needle' \
    assert_mcp_not_discloses d "$WITHOUT_NEEDLE" "$NEEDLE"
expect pass 'assert_mcp_ok passes a body without isError' \
    assert_mcp_ok d "$WITHOUT_NEEDLE"

# The body must really reproduce the trap, or the checks above prove nothing.
if echo "$WITH_NEEDLE" | grep -q "$NEEDLE"; then # e2e-pipe-ok: the trap itself
    echo "[selftest] warn: the old pipe did not hit SIGPIPE on this host: the test no longer discriminates"
fi

if [[ "$SELF_FAILURES" -gt 0 ]]; then
    echo "[selftest] FAIL — ${SELF_FAILURES} helper(s) answered wrong"
    exit 1
fi
echo "[selftest] PASS"
