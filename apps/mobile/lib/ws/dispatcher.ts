// Phase 7.3 — WebSocket event → React Query cache dispatcher.
// Phase 7.5 — also enqueues `<EventBanner>` events (WAKEUPEXPO §4.13).
//
// The client (`lib/ws/client.ts`) hands every inbound envelope to
// `applyWSEvent`. This module is the ONLY place that turns a server
// fact into a cache mutation — it never owns state itself; every
// fact still lives in TanStack Query (WAKEUPEXPO §4.4 / §6.2) — and
// the ONLY place that decides whether an event also surfaces a
// heads-up banner. The `<EventBanner>` component never filters; it
// just renders whatever this module queues.
//
// Cache actions:
//
//   1. invalidateQueries — the message-thread events
//      (`message.new` / `message.edited` / `message.deleted`) only
//      carry ids on the wire (`{ message_id, conversation_id,
//      sender_id, created_at }` — see backend `publishMessageEvent`),
//      not the body, so the open thread refetches; `message.new` also
//      invalidates the chats list (it's an infinite query whose pages
//      can't be re-sorted in place); `friend.*` / `conversation.*`
//      mark their lists stale the same way.
//   2. setQueryData patch — `presence.update` patches the friend's
//      presence row directly (the event carries its full state).
//   3. side-effect — `typing.start` / `typing.stop` poke
//      `useTypingStore` (after dropping the local user's own echo);
//      `room.*` will drive the call store in Phase 9 (no-op cases
//      for now).
//
// Banner enqueues (per §4.13): `message.new` (unless you're on that
// thread or it's muted), `friend.request_received`,
// `friend.request_accepted`, `conversation.member_added` (only when
// you're the added member). Globally suppressed when presence intent
// is `dnd`; `room.started` never banners (the CallOverlay owns it).
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
  InternalHandlerHttpPresenceListResponse,
  InternalHandlerHttpPresenceResponse,
} from '@/lib/api/model';
import { getActiveConversation } from '@/lib/banner/active-conversation';
import { getPresenceIntent } from '@/lib/banner/presence-intent';
import { enqueueBanner, type BannerEvent } from '@/lib/banner/store';
import { clearTyping, markTyping } from '@/lib/typing/store';
import type { WSEnvelope } from '@/lib/ws/client';

type ConversationList = InternalHandlerHttpConversationListResponse;
type Conversation = InternalHandlerHttpConversationResponse;
type PresenceList = InternalHandlerHttpPresenceListResponse;
type Presence = InternalHandlerHttpPresenceResponse;

// Extra, screen-derived context the WS bridge passes in: who the
// local user is (so member-added banners can tell "was *I* added").
// The "which conversation is on screen" check reads
// `getActiveConversation()` directly so it's always current.
export type DispatchContext = { myUserId?: string };

// URL-prefix query keys — mirror the orval `getGetV1*QueryKey()[0]`.
const CONVERSATIONS_KEY = '/v1/conversations';
const FRIENDS_KEY = '/v1/friends';
const FRIEND_REQUESTS_KEY = '/v1/friends/requests';
const PRESENCE_FRIENDS_KEY = '/v1/presence/friends';
const messagesKeyFor = (conversationId: string) => `/v1/conversations/${conversationId}/messages`;
const conversationDetailKeyFor = (conversationId: string) => `/v1/conversations/${conversationId}`;

// --- banner enqueue ------------------------------------------------

// Enqueue an event banner unless suppressed by the global gate that
// applies to every banner: the user's presence intent is `dnd`
// (§4.13 — same gate as pushes). Event-specific skips (on-screen,
// muted, not-the-added-member) are checked at the call site.
function maybeBanner(event: BannerEvent): void {
  if (getPresenceIntent() === 'dnd') return;
  enqueueBanner(event);
}

// --- conversations list --------------------------------------------

type Cached<P> = P | InfiniteData<P>;
function isInfinite<P>(d: Cached<P> | undefined): d is InfiniteData<P> {
  return !!d && Array.isArray((d as InfiniteData<P>).pages);
}

// Find a conversation row across every cached `/v1/conversations`
// query (the chats tab uses an infinite-query shape). Used to look up
// the sender's display name and the mute state for a message banner.
function findConversation(qc: QueryClient, id: string): Conversation | undefined {
  for (const [, data] of qc.getQueriesData<Cached<ConversationList>>({
    queryKey: [CONVERSATIONS_KEY],
  })) {
    if (!data) continue;
    const pages = isInfinite(data) ? data.pages : [data];
    for (const page of pages) {
      const hit = page.data?.find((c) => c.id === id);
      if (hit) return hit;
    }
  }
  return undefined;
}

function isMuted(c: Conversation | undefined): boolean {
  const until = c?.muted_until;
  return !!until && new Date(until).getTime() > Date.now();
}

function senderName(c: Conversation | undefined, senderId: string): string | undefined {
  const u = c?.members?.find((m) => m.user?.id === senderId)?.user;
  return u?.display_name?.trim() || u?.username?.trim() || undefined;
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

// Advance one member's read pointer inside a cached conversation
// detail. Returns the same object when nothing changed so React Query
// can skip the notify.
function patchMemberReadPointer(
  c: Conversation | undefined,
  userId: string,
  lastReadMessageId: string
): Conversation | undefined {
  if (!c?.members) return c;
  let touched = false;
  const members = c.members.map((m) => {
    if (m.user?.id !== userId || m.last_read_message_id === lastReadMessageId) return m;
    touched = true;
    return { ...m, last_read_message_id: lastReadMessageId };
  });
  return touched ? { ...c, members } : c;
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

// --- the dispatcher ------------------------------------------------

export function applyWSEvent(qc: QueryClient, env: WSEnvelope, ctx: DispatchContext = {}): void {
  switch (env.type) {
    case 'message.new': {
      // `{ message_id, conversation_id, sender_id, created_at }` — no
      // body on the wire, so both the open thread AND the chats list
      // refetch (the chats query is an infinite query whose pages
      // can't be re-sorted in place — a row may have to cross pages).
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      if (!d || !convId) return;
      void qc.invalidateQueries({ queryKey: [messagesKeyFor(convId)] });
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      // Banner — unless you're already on that thread or it's muted.
      const conv = findConversation(qc, convId);
      if (getActiveConversation() === convId || isMuted(conv)) return;
      const messageId = str(d.message_id);
      const name = senderName(conv, str(d.sender_id) ?? '');
      maybeBanner({
        id: messageId ?? `msg:${convId}:${str(d.created_at) ?? ''}`,
        title: name ? `New message from ${name}` : 'New message',
        route: `/conversations/${convId}`,
      });
      return;
    }
    case 'message.edited':
    case 'message.deleted': {
      // Same id-only wire shape — the open thread refetches to pick up
      // the new body / the deleted placeholder.
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
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
      const id = str(asRecord(env.data)?.id);
      maybeBanner({
        id: id ? `friend-req:${id}` : `friend-req:${Date.now()}`,
        title: 'New friend request',
        route: '/friends',
      });
      return;
    }
    case 'friend.request_accepted': {
      void qc.invalidateQueries({ queryKey: [FRIENDS_KEY] });
      void qc.invalidateQueries({ queryKey: [FRIEND_REQUESTS_KEY] });
      const id = str(asRecord(env.data)?.id);
      maybeBanner({
        id: id ? `friend-acc:${id}` : `friend-acc:${Date.now()}`,
        title: 'Friend request accepted',
        route: '/friends',
      });
      return;
    }
    case 'conversation.member_added': {
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      // Banner only when *you* were the one added (payload is
      // `{ conversation_id, member: { user: { id, … }, … } }`).
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const member = d && asRecord(d.member);
      const addedUser = member && asRecord(member.user);
      const addedId = addedUser && str(addedUser.id);
      if (!convId || !ctx.myUserId || addedId !== ctx.myUserId) return;
      const name = findConversation(qc, convId)?.name?.trim();
      maybeBanner({
        id: `member-added:${convId}`,
        title: name ? `Added you to ${name}` : 'Added you to a group',
        route: `/conversations/${convId}`,
      });
      return;
    }
    case 'conversation.created':
    case 'conversation.updated':
    case 'conversation.member_removed': {
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      return;
    }
    case 'typing.start':
    case 'typing.stop': {
      // Payload is `{ conversation_id, user_id }` (server fills
      // user_id on the S→C path). Drop our own echo, then poke the
      // typing store; `<TypingIndicator>` renders off it.
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const userId = d && str(d.user_id);
      if (!convId || !userId || userId === ctx.myUserId) return;
      if (env.type === 'typing.start') markTyping(convId, userId);
      else clearTyping(convId, userId);
      return;
    }
    case 'message.read': {
      // Payload `{ conversation_id, user_id, last_read_message_id }`
      // — someone (maybe us, from another device) advanced their read
      // pointer. Patch that member's row in the cached conversation
      // detail so the open thread's §6.3 receipt captions recompute.
      // No banner; no message refetch (the body didn't change).
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const userId = d && str(d.user_id);
      const readId = d && str(d.last_read_message_id);
      if (!convId || !userId || !readId) return;
      qc.setQueryData<Conversation>([conversationDetailKeyFor(convId)], (cur) =>
        patchMemberReadPointer(cur, userId, readId)
      );
      return;
    }
    // --- known events handled by later phases (deliberate no-ops) ---
    case 'room.started': // call store + RoomBanner — Phase 9 (no banner: CallOverlay owns it)
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
