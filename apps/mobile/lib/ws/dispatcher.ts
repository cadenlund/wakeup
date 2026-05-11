// Phase 7.3 — WebSocket event → React Query cache dispatcher.
//
// The client (`lib/ws/client.ts`) hands every inbound envelope to
// `applyWSEvent`. This module is the ONLY place that turns a server
// fact into a cache mutation — it never owns state itself; every
// fact still lives in TanStack Query (WAKEUPEXPO §4.4 / §6.2).
//
// Three kinds of action (§6.2):
//
//   1. setQueryData patch — `message.new` (prepend), `message.edited`,
//      `message.deleted` patch the cached message pages directly so the
//      open thread updates without a refetch.
//   2. invalidateQueries — `friend.*` and `conversation.*` mark the
//      relevant lists stale so they re-fetch on next render.
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
import type { InfiniteData, QueryClient } from '@tanstack/react-query';

import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpMessageResponse,
  InternalHandlerHttpPresenceListResponse,
  InternalHandlerHttpPresenceResponse,
} from '@/lib/api/model';
import type { WSEnvelope } from '@/lib/ws/client';

type Message = InternalHandlerHttpMessageResponse;
type MessageList = InternalHandlerHttpMessageListResponse;
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

// A cached list query may be either a single page (`{ data }`) or the
// `useInfiniteQuery` shape (`{ pages: [{ data }, …] }`); the message
// thread uses the latter. These helpers patch both transparently —
// same approach `lib/use-send-message.ts` takes for optimistic sends.
type Infinite<P> = InfiniteData<P>;
type Cached<P> = P | Infinite<P>;

function isInfinite<P>(data: Cached<P> | undefined): data is Infinite<P> {
  return !!data && Array.isArray((data as Infinite<P>).pages);
}

// Apply `fn` to every page of a cached list (or the single page),
// returning a new object only if something actually changed so React
// Query can skip a re-render when the event was a no-op.
function mapPages<P>(data: Cached<P> | undefined, fn: (page: P) => P): Cached<P> | undefined {
  if (!data) return data;
  if (isInfinite(data)) {
    let touched = false;
    const pages = data.pages.map((p) => {
      const next = fn(p);
      if (next !== p) touched = true;
      return next;
    });
    return touched ? { ...data, pages } : data;
  }
  return fn(data);
}

// --- message.* -----------------------------------------------------

function messageInPage(page: MessageList, id: string): boolean {
  return !!page.data?.some((m) => m.id === id);
}

// Prepend a freshly-arrived message to the first page. Skips if a row
// with that id is already cached — covers the echo of the local
// user's own send (the POST response already inserted it) and any
// duplicate delivery.
function prependMessage(
  data: Cached<MessageList> | undefined,
  msg: Message
): Cached<MessageList> | undefined {
  const id = msg.id;
  if (!data || !id) return data;
  // Already present anywhere → no-op.
  const present = isInfinite(data)
    ? data.pages.some((p) => messageInPage(p, id))
    : messageInPage(data, id);
  if (present) return data;
  if (isInfinite(data)) {
    if (data.pages.length === 0) return { ...data, pages: [{ data: [msg] } as MessageList] };
    const [first, ...rest] = data.pages;
    return { ...data, pages: [{ ...first, data: [msg, ...(first.data ?? [])] }, ...rest] };
  }
  return { ...data, data: [msg, ...(data.data ?? [])] };
}

// Replace a cached message with `patch` merged on top — used for
// `message.edited` (new body + edited_at) and `message.deleted`
// (is_deleted/deleted_at, body blanked server-side).
function patchMessage(
  data: Cached<MessageList> | undefined,
  id: string,
  patch: Partial<Message>
): Cached<MessageList> | undefined {
  return mapPages<MessageList>(data, (page) => {
    if (!page.data) return page;
    let touched = false;
    const next = page.data.map((m) => {
      if (m.id !== id) return m;
      touched = true;
      return { ...m, ...patch };
    });
    return touched ? { ...page, data: next } : page;
  });
}

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

// --- the dispatcher ------------------------------------------------

export function applyWSEvent(qc: QueryClient, env: WSEnvelope): void {
  switch (env.type) {
    case 'message.new': {
      // `data` is the message DTO (server marshals the response shape).
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const id = d && str(d.id);
      if (!d || !convId || !id) return;
      const msg = d as Message;
      qc.setQueriesData<Cached<MessageList>>({ queryKey: [messagesKeyFor(convId)] }, (cur) =>
        prependMessage(cur, msg)
      );
      qc.setQueriesData<ConversationList>({ queryKey: [CONVERSATIONS_KEY] }, (cur) =>
        bumpConversation(cur, convId, msg.created_at)
      );
      return;
    }
    case 'message.edited': {
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const id = d && str(d.id);
      if (!d || !convId || !id) return;
      qc.setQueriesData<Cached<MessageList>>({ queryKey: [messagesKeyFor(convId)] }, (cur) =>
        patchMessage(cur, id, { body: str(d.body), edited_at: str(d.edited_at) })
      );
      return;
    }
    case 'message.deleted': {
      // Payload is `{ message_id, conversation_id }`.
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const id = d && str(d.message_id);
      if (!d || !convId || !id) return;
      qc.setQueriesData<Cached<MessageList>>({ queryKey: [messagesKeyFor(convId)] }, (cur) =>
        patchMessage(cur, id, { is_deleted: true, body: '' })
      );
      return;
    }
    case 'presence.update': {
      // Payload is `{ user_id, status, last_active_at }`.
      const d = asRecord(env.data);
      const userId = d && str(d.user_id);
      if (!d || !userId) return;
      qc.setQueriesData<PresenceList>({ queryKey: [PRESENCE_FRIENDS_KEY] }, (cur) =>
        patchPresence(cur, userId, { status: str(d.status), last_active_at: str(d.last_active_at) })
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
