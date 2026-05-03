#!/usr/bin/env bash
# scripts/ci/check-ws-tests.sh — §12.7 CI gate.
#
# Greps the WebSocket test files for subtest naming patterns that match
# every event in §7.2. For each event we expect at least the four
# core matrix rows: fires_for_recipients, does_not_fire_for_outsiders,
# payload_shape, multi_instance_fanout. (idempotent_under_repeat is
# event-specific — handled per-event below where the spec defines it.)
#
# Phase 8.4 ships coverage for the events the backend currently
# publishes (`message.new`, `typing.start`, `typing.stop`). Events
# that aren't wired yet — `presence.update`, `friend.*`, `room.*`,
# the rest of the message.* family — are listed in PENDING_EVENTS
# and the script merely warns. Phases 9 / 10 graduate them into
# REQUIRED_EVENTS as the underlying services land.
#
# Usage: scripts/ci/check-ws-tests.sh
# Exit codes: 0 = pass, 1 = required coverage missing.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WS_DIR="$ROOT/apps/backend/internal/handler/ws"

# Required events: every row in §7.2 with a complete 4-subtest matrix
# in matrix_test.go. As phases land more service-side publishes AND
# their matrix tests, move events from PENDING_EVENTS into here.
REQUIRED_EVENTS=(
  "MessageNew"
)

# Pending events: §7.2 rows whose 4-subtest matrix isn't fully present
# yet (either the service-side publish isn't wired, or partial
# coverage exists but doesn't satisfy all four rows). The script
# warns on these so the gap stays visible without blocking CI.
PENDING_EVENTS=(
  "MessageEdited"        # service publishes; matrix subtests TODO
  "MessageDeleted"       # service publishes; matrix subtests TODO
  "MessageRead"          # not yet published
  "ConversationCreated"  # not yet published
  "ConversationUpdated"  # not yet published
  "ConversationMemberAdded"      # not yet published
  "ConversationMemberRemoved"    # not yet published
  "PresenceUpdate"       # Phase 9
  "TypingStart"          # has FiresForRecipients + PayloadShape; outsiders/multi-inst TODO
  "TypingStop"           # symmetrical with TypingStart
  "FriendRequestReceived"  # Phase 5 events not yet published to WS
  "FriendRequestAccepted"  # Phase 5
  "RoomStarted"          # Phase 10
  "RoomParticipantJoined" # Phase 10
  "RoomParticipantLeft"  # Phase 10
  "RoomVideoChanged"     # Phase 10
  "RoomEnded"            # Phase 10
)

# Subtest patterns we expect per event (the §12.7 core matrix).
REQUIRED_SUBTESTS=(
  "FiresForRecipients"
  "DoesNotFireForOutsiders"
  "PayloadShape"
  "MultiInstanceFanout"
)

fail=0
warn=0

# Each (event, subtest) pair is a top-level Go test name like
# `TestMessageNew_FiresForRecipients`. Grep for the func declaration
# AND check the body isn't just t.Skip — a stub doesn't satisfy the
# §12.7 matrix even if it grep-matches. (CodeRabbit PR #50.)
#
# is_real_test name → 0 if a real (non-skipped) func exists in WS_DIR.
is_real_test() {
  local name="$1"
  awk -v name="$name" '
    BEGIN { in_func = 0; depth = 0; skipped = 0; saw = 0 }
    {
      if (!in_func) {
        if ($0 ~ "^func " name "\\(") {
          in_func = 1
          saw = 1
          # Count braces on the func line itself (rare but possible).
          opens = gsub(/\{/, "{")
          closes = gsub(/\}/, "}")
          depth = opens - closes
          next
        }
      } else {
        opens = gsub(/\{/, "{")
        closes = gsub(/\}/, "}")
        depth += opens - closes
        if ($0 ~ /t\.Skip\(/) skipped = 1
        if (depth <= 0) {
          if (saw && !skipped) { found = 1; exit }
          in_func = 0
          saw = 0
          skipped = 0
        }
      }
    }
    END { exit found ? 0 : 1 }
  ' "$WS_DIR"/*.go
}

echo "== §12.7 WebSocket test matrix coverage =="
for event in "${REQUIRED_EVENTS[@]}"; do
  for subtest in "${REQUIRED_SUBTESTS[@]}"; do
    name="Test${event}_${subtest}"
    if ! is_real_test "$name"; then
      printf 'MISSING (required): %s\n' "$name"
      fail=$((fail + 1))
    fi
  done
done

echo
echo "== Pending events (not yet wired in backend services) =="
for event in "${PENDING_EVENTS[@]}"; do
  for subtest in "${REQUIRED_SUBTESTS[@]}"; do
    pattern="^func Test${event}_${subtest}\b"
    if ! grep -RE "$pattern" "$WS_DIR" >/dev/null 2>&1; then
      printf 'pending: Test%s_%s (will be required when its service publishes)\n' "$event" "$subtest"
      warn=$((warn + 1))
    fi
  done
done

# Connection-lifecycle subtests: §12.7 lifecycle subtable. Each must
# appear as a t.Run within TestWebSocketLifecycle. Phase 8.4 covers
# the four most-foundational items; phases 9 (presence) and the
# rate-limit milestone fill in heartbeat/decay/typing-debounce.
echo
echo "== TestWebSocketLifecycle subtests =="
LIFECYCLE_REQUIRED=(
  "simultaneous_connections_same_user"
  "reconnect_no_replay"
)
LIFECYCLE_PENDING=(
  "upgrade_no_cookie"           # stubbed in TestWebSocketLifecycle; covered by TestWSHandler_UnauthenticatedRejected. Graduate to required when the lifecycle subtest body is real (CodeRabbit PR #50).
  "upgrade_valid_cookie"        # same — covered by TestWSHandler_AuthenticatedDialSucceeds.
  "upgrade_expired_cookie"      # Phase 12 (session expiry tests)
  "upgrade_tampered_cookie"     # Phase 12
  "heartbeat_updates_db"        # Phase 9 (presence)
  "stale_presence_decays"       # Phase 9
  "typing_debounce"             # Phase 9 / typing service
  "disconnect_publishes_presence_change"  # Phase 9
  "slow_consumer_kicked"        # covered by hub_test, listed for completeness
)
# Same is-real check for lifecycle t.Run subtests: a stub that calls
# t.Skip immediately doesn't earn a "covered" tick.
is_real_subtest() {
  local name="$1"
  awk -v name="$name" '
    BEGIN { in_sub = 0; depth = 0; skipped = 0; saw = 0 }
    {
      if (!in_sub) {
        # Match: t.Run("upgrade_no_cookie", func(...) {
        if ($0 ~ "t\\.Run\\(\"" name "\"") {
          in_sub = 1
          saw = 1
          opens = gsub(/\{/, "{")
          closes = gsub(/\}/, "}")
          depth = opens - closes
          next
        }
      } else {
        opens = gsub(/\{/, "{")
        closes = gsub(/\}/, "}")
        depth += opens - closes
        if ($0 ~ /t\.Skip\(/) skipped = 1
        if (depth <= 0) {
          if (saw && !skipped) { found = 1; exit }
          in_sub = 0
          saw = 0
          skipped = 0
        }
      }
    }
    END { exit found ? 0 : 1 }
  ' "$WS_DIR"/*.go
}
for sub in "${LIFECYCLE_REQUIRED[@]}"; do
  if ! is_real_subtest "$sub"; then
    printf 'MISSING (required): TestWebSocketLifecycle/%s\n' "$sub"
    fail=$((fail + 1))
  fi
done
for sub in "${LIFECYCLE_PENDING[@]}"; do
  pattern="t\.Run\(\"${sub}\""
  if ! grep -RE "$pattern" "$WS_DIR" >/dev/null 2>&1; then
    printf 'pending: TestWebSocketLifecycle/%s\n' "$sub"
    warn=$((warn + 1))
  fi
done

echo
if [[ "$fail" -gt 0 ]]; then
  echo "FAIL: $fail required matrix entries missing ($warn pending — informational)" >&2
  exit 1
fi
echo "OK: required matrix complete ($warn pending — informational)"
exit 0
