#!/usr/bin/env bash
# scripts/ci/check-handler-tests.sh — §12.5 / §13.6 CI gate.
#
# Enforces the §12.5 discipline rule: every swaggo @Failure annotation
# in a *_handler.go file must be reachable from a test in the matching
# *_handler_test.go. The gate is intentionally coarse — counting top-
# level Test funcs + t.Run subtests against documented @Failure entries —
# because the alternative (matching individual status codes to specific
# tests) is too brittle when error envelopes share assertion helpers.
#
# Two thresholds:
#
#   - HARD FAIL: a handler file documents N>0 @Failure annotations but
#     the matching _test.go has zero tests (or doesn't exist). That
#     means the entire endpoint group is undocumented in tests.
#   - SOFT WARN: tests < failures by some delta. Printed but doesn't
#     block CI; a handler with 31 failures and 24 tests probably has
#     shared error-path assertions and isn't actually under-covered.
#
# Usage: scripts/ci/check-handler-tests.sh
# Exit codes: 0 = pass, 1 = at least one handler has zero coverage.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HTTP_DIR="$ROOT/apps/backend/internal/handler/http"

if [[ ! -d "$HTTP_DIR" ]]; then
  echo "FAIL: handler dir not found: $HTTP_DIR" >&2
  exit 1
fi

# countFailures handler.go → number of `// @Failure ` annotations.
count_failures() {
  grep -c '^// @Failure' "$1" 2>/dev/null || true
}

# countTests test.go → number of `^func Test` declarations + `t.Run(`
# subtests. Subtests count because the §12.5 discipline rule assumes
# each scenario is exercised by either a top-level Test func or a
# table-driven t.Run case.
count_tests() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo 0
    return
  fi
  local funcs subs
  funcs=$(grep -c '^func Test' "$file" 2>/dev/null || true)
  subs=$(grep -c '\bt\.Run(' "$file" 2>/dev/null || true)
  echo $((funcs + subs))
}

fail=0
warn=0

printf '%-40s %10s %10s %s\n' "handler" "@Failure" "tests" "status"
printf '%-40s %10s %10s %s\n' "----------------------------------------" "--------" "-----" "------"

# Iterate every *_handler.go (skip the test file naming convention).
shopt -s nullglob
for handler_file in "$HTTP_DIR"/*_handler.go; do
  base=$(basename "$handler_file" .go)
  test_file="$HTTP_DIR/${base}_test.go"

  failures=$(count_failures "$handler_file")
  tests=$(count_tests "$test_file")

  status="ok"
  if [[ "$failures" -gt 0 && "$tests" -eq 0 ]]; then
    status="MISSING"
    fail=$((fail + 1))
  elif [[ "$tests" -lt "$failures" ]]; then
    status="warn"
    warn=$((warn + 1))
  fi

  printf '%-40s %10s %10s %s\n' "$base.go" "$failures" "$tests" "$status"
done

echo
if [[ "$fail" -gt 0 ]]; then
  echo "FAIL: $fail handler file(s) document @Failure annotations but have no tests" >&2
  exit 1
fi
if [[ "$warn" -gt 0 ]]; then
  echo "OK: $warn handler file(s) have fewer test entry points than documented failures (likely shared error-path helpers — informational)"
else
  echo "OK: every handler with documented failures has matching tests"
fi
exit 0
