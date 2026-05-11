// Phase 7.3 — WebSocket event → React Query cache dispatcher.
//
// The client (`lib/ws/client.ts`) hands every inbound envelope to
// `applyWSEvent`. This module is the ONLY place that turns a server
// fact into a cache mutation — it never owns state itself; every
// fact still lives in TanStack Query (WAKEUPEXPO §4.4 / §6.2).
//
// Two kinds of action:
//
//   1. invalidateQueries — the message-thread events
//      (`message.new` / `message.edited` / `message.deleted`) only
//      carry ids on the wire (`{ message_id, conversation_id,
//      sender_id, created_at }` — see backend `publishMessageEvent`),
//      not the body, so the open thread refetches; `friend.*` and
//      `conversation.*` mark their lists stale the same way.
//   2. setQueryData patch — the events that DO carry their full state:
//      `message.new` also bumps the conversation row's
//      `last_message_at` and re-sorts the chats list; `presence.update`
//      patches the friend's presence row.
//   3. side-effect — `room.*` (call store), `typing.*` (typing store).
//      Those subsystems land in later phases; the cases are present as
//      explicit no-ops so an arriving event is silently ignored rather
//      than logged as "unknown".
//
// Unhandled-but-known events (`message.read`, `notification.new`, …)
// are deliberate no-ops too — they get wired when their feature lands.
// A genuinely unknown `type` (one the server added that this build
// doesn't know) is logged once at warn so the gap is visible.
//
// Query keys are inlined as URL-prefix literals rather than imported
// from the orval-generated hook modules — those modules transitively
// pull in `@/lib/env` → `react-native`, which would tie this pure
// module (and its `bun test` suite) to the RN runtime. The literals
// mirror `getGetV1*QueryKey()[0]` and are part of the stable API
// contract (the same trick `lib/use-send-message.ts` already uses).
import type { QueryClient } from '@tanstack/react-query';

import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpPresenceListResponse,
  InternalHandlerHttpPresenceResponse,
} from '@/lib/api/model';
import type { WSEnvelope } from '@/lib/ws/client';

type ConversationList = InternalHandlerHttpConversationListResponse;
type Conversation = InternalHandlerHttpConversationResponse;
type PresenceList = InternalHandlerHttpPresenceListResponse;
type Presence = InternalHandlerHttpPresenceResponse;

// URL-prefix query keys — mirror the orval `getGetV1*QueryKey()[0]`.
const CONVERSATIONS_KEY = '/v1/conversations';
const FRIENDS_KEY = '/v1/friends';
const FRIEND_REQUESTS_KEY = '/v1/friends/requests';
const PRESENCE_FRIENDS_KEY = '/v1/presence/friends';
const messagesKeyFor = (conversationId: string) => `/v1/conversations/${conversationId}/messages`;

// --- conversations list --------------------------------------------

// Bump a conversation row's last_message_at and re-sort so the thread
// jumps to the top of the chats list when a new message lands there.
function bumpConversation(
  data: ConversationList | undefined,
  conversationId: string,
  lastMessageAt: string | undefined
): ConversationList | undefined {
  if (!data?.data || !lastMessageAt) return data;
  let touched = false;
  const rows: Conversation[] = data.data.map((c) => {
    if (c.id !== conversationId) return c;
    touched = true;
    return { ...c, last_message_at: lastMessageAt };
  });
  if (!touched) return data;
  // Pinned rows stay above unpinned; within each band, newest
  // last_message_at first — mirrors the server's list ordering.
  rows.sort((a, b) => {
    const ap = a.pinned_at ? 1 : 0;
    const bp = b.pinned_at ? 1 : 0;
    if (ap !== bp) return bp - ap;
    return (b.last_message_at ?? '').localeCompare(a.last_message_at ?? '');
  });
  return { ...data, data: rows };
}

// --- presence ------------------------------------------------------

function patchPresence(
  data: PresenceList | undefined,
  userId: string,
  patch: Partial<Presence>
): PresenceList | undefined {
  if (!data?.data) return data;
  let touched = false;
  const next = data.data.map((p) => {
    if (p.user_id !== userId) return p;
    touched = true;
    return { ...p, ...patch };
  });
  return touched ? { ...data, data: next } : data;
}

// --- payload guards ------------------------------------------------
//
// `data` is `unknown` off the wire; narrow defensively — a malformed
// payload should be ignored, never throw.

function asRecord(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : null;
}
function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}

// Build a patch object from `src`, keeping only keys whose value is a
// string. A missing/non-string field is OMITTED, never set to
// `undefined` — `{ ...row, ...patch }` would otherwise erase a
// previously-valid value on a malformed payload (CR on PR #151).
function stringPatch<K extends string>(
  src: Record<string, unknown>,
  keys: readonly K[]
): Record<K, string> {
  const out = {} as Record<K, string>;
  for (const k of keys) {
    const v = str(src[k]);
    if (v !== undefined) out[k] = v;
  }
  return out;
}

// The shared shape of the three message-thread events on the wire.
function messageEventConversationId(env: WSEnvelope): string | undefined {
  const d = asRecord(env.data);
  return d ? str(d.conversation_id) : undefined;
}

// --- the dispatcher ------------------------------------------------

export function applyWSEvent(qc: QueryClient, env: WSEnvelope): void {
  switch (env.type) {
    case 'message.new': {
      // `{ message_id, conversation_id, sender_id, created_at }` — no
      // body on the wire, so the thread refetches; the chats list gets
      // an in-place bump so it re-sorts without a round-trip.
      const convId = messageEventConversationId(env);
      if (!convId) return;
      void qc.invalidateQueries({ queryKey: [messagesKeyFor(convId)] });
      const createdAt = str(asRecord(env.data)?.created_at);
      qc.setQueriesData<ConversationList>({ queryKey: [CONVERSATIONS_KEY] }, (cur) =>
        bumpConversation(cur, convId, createdAt)
      );
      return;
    }
    case 'message.edited':
    case 'message.deleted': {
      // Same id-only wire shape — the open thread refetches to pick up
      // the new body / the deleted placeholder.
      const convId = messageEventConversationId(env);
      if (!convId) return;
      void qc.invalidateQueries({ queryKey: [messagesKeyFor(convId)] });
      return;
    }
    case 'presence.update': {
      // Payload is `{ user_id, status, last_active_at }`.
      const d = asRecord(env.data);
      const userId = d && str(d.user_id);
      if (!d || !userId) return;
      const patch = stringPatch(d, ['status', 'last_active_at'] as const);
      if (Object.keys(patch).length === 0) return;
      qc.setQueriesData<PresenceList>({ queryKey: [PRESENCE_FRIENDS_KEY] }, (cur) =>
        patchPresence(cur, userId, patch)
      );
      return;
    }
    case 'friend.request_received': {
      void qc.invalidateQueries({ queryKey: [FRIEND_REQUESTS_KEY] });
      // The <EventBanner> enqueue for this event lands in Phase 7.5.
      return;
    }
    case 'friend.request_accepted': {
      void qc.invalidateQueries({ queryKey: [FRIENDS_KEY] });
      void qc.invalidateQueries({ queryKey: [FRIEND_REQUESTS_KEY] });
      // The <EventBanner> enqueue for this event lands in Phase 7.5.
      return;
    }
    case 'conversation.created':
    case 'conversation.updated':
    case 'conversation.member_added':
    case 'conversation.member_removed': {
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      // member_added/removed for the open thread also touches its
      // detail query; the conversation/[id] screen invalidates that
      // on focus, so a list invalidation is enough here.
      return;
    }
    // --- known events handled by later phases (deliberate no-ops) ---
    case 'message.read': // read receipts — Phase 6.3 rendering wiring
    case 'typing.start': // typing store — Phase 6.4
    case 'typing.stop':
    case 'room.started': // call store + RoomBanner — Phase 9
    case 'room.participant_joined':
    case 'room.participant_left':
    case 'room.video_changed':
    case 'room.ended':
    case 'notification.new': // activity feed — Phase 8.8
      return;
    default:
      // A type this build doesn't recognise — surface it so a
      // server-added event that needs handling doesn't vanish.
      console.warn('[ws.dispatcher] unhandled event type', env.type);
      return;
  }
}
