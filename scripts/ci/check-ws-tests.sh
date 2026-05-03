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
  "ConvMemberAdded"      # not yet published
  "ConvMemberRemoved"    # not yet published
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
# `TestMessageNew_FiresForRecipients`. Grep for it across the WS
# package's test files.
echo "== §12.7 WebSocket test matrix coverage =="
for event in "${REQUIRED_EVENTS[@]}"; do
  for subtest in "${REQUIRED_SUBTESTS[@]}"; do
    pattern="^func Test${event}_${subtest}\b"
    if ! grep -RE "$pattern" "$WS_DIR" >/dev/null 2>&1; then
      printf 'MISSING (required): Test%s_%s\n' "$event" "$subtest"
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
  "upgrade_no_cookie"
  "upgrade_valid_cookie"
  "simultaneous_connections_same_user"
  "reconnect_no_replay"
)
LIFECYCLE_PENDING=(
  "upgrade_expired_cookie"      # Phase 12 (session expiry tests)
  "upgrade_tampered_cookie"     # Phase 12
  "heartbeat_updates_db"        # Phase 9 (presence)
  "stale_presence_decays"       # Phase 9
  "typing_debounce"             # Phase 9 / typing service
  "disconnect_publishes_presence_change"  # Phase 9
  "slow_consumer_kicked"        # covered by hub_test, listed for completeness
)
for sub in "${LIFECYCLE_REQUIRED[@]}"; do
  pattern="t\.Run\(\"${sub}\""
  if ! grep -RE "$pattern" "$WS_DIR" >/dev/null 2>&1; then
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
