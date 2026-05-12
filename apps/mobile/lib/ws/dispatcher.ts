// Phase 7.3 — WebSocket event → React Query cache dispatcher.
// Phase 7.5 — also enqueues heads-up events for the toast surface
// (WAKEUPEXPO §4.13).
//
// The client (`lib/ws/client.ts`) hands every inbound envelope to
// `applyWSEvent`. This module is the ONLY place that turns a server
// fact into a cache mutation — it never owns state itself; every
// fact still lives in TanStack Query (WAKEUPEXPO §4.4 / §6.2) — and
// the ONLY place that decides whether an event also surfaces a
// heads-up notification. It enqueues into `useBannerStore`;
// `<EventToastBridge>` drains that into `toast.event(...)` (the
// dispatcher stays off `react-native` so its `bun test` suite runs,
// hence the store seam rather than calling `toast` directly).
//
// Cache actions:
//
//   1. invalidateQueries — the message-thread events
//      (`message.new` / `message.edited` / `message.deleted`) carry
//      ids (+ `body` on new/edited, used only for the banner preview)
//      — not the full DTO — so the open thread still refetches;
//      `message.new` also invalidates the chats list (it's an
//      infinite query whose pages can't be re-sorted in place);
//      `friend.*` / `conversation.*` mark their lists stale the same way.
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
import { setBadgeUnreadTotal } from '@/lib/badge/store';
import { getActiveConversation } from '@/lib/banner/active-conversation';
import { getPresenceIntent } from '@/lib/banner/presence-intent';
import { enqueueBanner, type BannerEvent } from '@/lib/banner/store';
import { conversationDisplay } from '@/lib/conversation-display';
import { invalidateRelationships } from '@/lib/friend-cache';
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
// Friend-graph keys live in `lib/friend-cache.ts` (`invalidateRelationships`).
const CONVERSATIONS_KEY = '/v1/conversations';
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

function senderUser(c: Conversation | undefined, senderId: string) {
  return c?.members?.find((m) => m.user?.id === senderId)?.user;
}
function displayName(u: ReturnType<typeof senderUser>): string | undefined {
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
// Best-effort display name from a `WSUser`-ish payload object
// (`{ display_name, username }`). Falls back to "Someone".
function wsUserName(v: unknown): string {
  const u = asRecord(v);
  return (str(u?.display_name) ?? '').trim() || (str(u?.username) ?? '').trim() || 'Someone';
}
// Best-effort trimmed string from an unknown payload field.
function trimmedStr(v: unknown): string {
  return (str(v) ?? '').trim();
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
      // `{ message_id, conversation_id, sender_id, created_at, body }`
      // — the body rides along (for the banner preview), but we still
      // refetch the open thread + chats list rather than patch: the
      // thread query needs the full DTO (attachments, reply_to, …) and
      // the chats query is an infinite query whose pages can't be
      // re-sorted in place (a row may have to cross pages).
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      if (!d || !convId) return;
      void qc.invalidateQueries({ queryKey: [messagesKeyFor(convId)] });
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      // Banner — unless you're already on that thread or it's muted.
      const conv = findConversation(qc, convId);
      if (getActiveConversation() === convId || isMuted(conv)) return;
      const messageId = str(d.message_id);
      const body = str(d.body);
      const sender = displayName(senderUser(conv, str(d.sender_id) ?? ''));
      // Use the SAME visual identity the chats list shows: a DM is the
      // peer (name + avatar); a group is its name + photo, or — if it
      // has neither — the "Ada, Ben and 2 more" preview + the
      // overlapping-member cluster. The body keeps the sender prefix in
      // a group so you still see who said it.
      const disp = conv ? conversationDisplay(conv, ctx.myUserId, new Map()) : undefined;
      const isGroupConv = conv?.type === 'group';
      maybeBanner({
        id: messageId ?? `msg:${convId}:${str(d.created_at) ?? ''}`,
        title: disp?.title ?? 'New message',
        body: isGroupConv && sender && body ? `${sender}: ${body}` : body,
        route: `/conversations/${convId}`,
        avatar: disp
          ? {
              avatarUrl: disp.avatarUrl,
              fallbackInitial: disp.fallbackInitial,
              stackedMembers: disp.stackedMembers,
            }
          : undefined,
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
      // `{ request_id, user: { id, username, display_name } }` — `user`
      // is the requester. No avatar URL on the wire (needs presigning),
      // so the toast shows their initials avatar. Refresh the whole
      // relationship surface so e.g. an open search row updates.
      void invalidateRelationships(qc);
      const d = asRecord(env.data);
      const reqId = str(d?.request_id);
      const name = wsUserName(d?.user);
      maybeBanner({
        id: reqId ? `friend-req:${reqId}` : `friend-req:${Date.now()}`,
        title: name,
        body: 'Sent you a friend request',
        route: '/friends',
        avatar: { fallbackInitial: name },
      });
      return;
    }
    case 'friend.request_accepted': {
      // `{ request_id, user }` — `user` is the person who accepted.
      void invalidateRelationships(qc);
      const d = asRecord(env.data);
      const reqId = str(d?.request_id);
      const name = wsUserName(d?.user);
      maybeBanner({
        id: reqId ? `friend-acc:${reqId}` : `friend-acc:${Date.now()}`,
        title: name,
        body: 'Accepted your friend request',
        route: '/friends',
        avatar: { fallbackInitial: name },
      });
      return;
    }
    case 'conversation.member_added': {
      void qc.invalidateQueries({ queryKey: [CONVERSATIONS_KEY] });
      // Payload `{ conversation_id, conversation_name, member: { id, … } }`.
      // Banner only when *you* were the one added; existing members
      // just get the cache nudge above (+ the detail refresh below) so
      // the new face shows up.
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      if (convId) {
        void qc.invalidateQueries({ queryKey: [conversationDetailKeyFor(convId)] });
      }
      const addedId = str(asRecord(d?.member)?.id);
      if (!convId || !ctx.myUserId || addedId !== ctx.myUserId) return;
      const groupName = trimmedStr(d?.conversation_name) || 'a group';
      maybeBanner({
        id: `member-added:${convId}`,
        title: groupName,
        body: 'You were added to this group',
        route: `/conversations/${convId}`,
        avatar: { fallbackInitial: groupName },
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
      // Payload `{ conversation_id, message_id, user_id, read_at }`
      // (§7.2) — `message_id` is the reader's new last-read pointer.
      // Maybe us (another device). Patch that member's row in the
      // cached conversation detail so the open thread's §6.3 receipt
      // captions recompute. No banner; no message refetch.
      const d = asRecord(env.data);
      const convId = d && str(d.conversation_id);
      const userId = d && str(d.user_id);
      const readId = d && str(d.message_id);
      if (!convId || !userId || !readId) return;
      qc.setQueryData<Conversation>([conversationDetailKeyFor(convId)], (cur) =>
        patchMemberReadPointer(cur, userId, readId)
      );
      return;
    }
    case 'heartbeat': {
      // Server ack to the client's keepalive ping — `data` carries
      // `{ unread_total }` (omitted when 0). Stashed in the badge
      // store; the RN-side <PushNotifications/> bridge mirrors it onto
      // the app-icon badge (§7.5). No cache mutation, no banner.
      const d = asRecord(env.data);
      const total = d && typeof d.unread_total === 'number' ? d.unread_total : 0;
      setBadgeUnreadTotal(total);
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
