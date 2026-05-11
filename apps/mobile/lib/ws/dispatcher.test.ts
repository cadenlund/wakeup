// Phase 7.3 — per-event dispatcher unit tests.
//
// Each test seeds a real QueryClient, fires one WS envelope through
// `applyWSEvent`, and asserts the resulting cache state. No React, no
// network — the dispatcher is pure (QueryClient in, cache mutation
// out), so it tests in isolation.
import { describe, expect, test } from 'bun:test';
import { QueryClient } from '@tanstack/react-query';

import type {
  InternalHandlerHttpConversationListResponse,
  InternalHandlerHttpMessageListResponse,
  InternalHandlerHttpPresenceListResponse,
} from '@/lib/api/model';
import { applyWSEvent } from '@/lib/ws/dispatcher';

type MessageList = InternalHandlerHttpMessageListResponse;
type InfiniteMessages = { pages: MessageList[]; pageParams: unknown[] };
type ConversationList = InternalHandlerHttpConversationListResponse;
type PresenceList = InternalHandlerHttpPresenceListResponse;

const CONV = 'conv-1';
// Mirrors `useInfiniteMessages`' key: ['/v1/conversations/{id}/messages', {limit,q}, 'infinite'].
const messagesKey = [`/v1/conversations/${CONV}/messages`, { limit: 20, q: undefined }, 'infinite'];
const conversationsKey = ['/v1/conversations'];
const presenceKey = ['/v1/presence/friends'];

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function seedMessages(qc: QueryClient, pages: MessageList[]): void {
  qc.setQueryData<InfiniteMessages>(messagesKey, { pages, pageParams: [undefined] });
}
function readMessages(qc: QueryClient): InfiniteMessages | undefined {
  return qc.getQueryData<InfiniteMessages>(messagesKey);
}

describe('applyWSEvent — message.new', () => {
  test('prepends to the first page', () => {
    const qc = newClient();
    seedMessages(qc, [{ data: [{ id: 'm0', body: 'old', created_at: '2026-01-01T00:00:00Z' }] }]);
    applyWSEvent(qc, {
      type: 'message.new',
      data: { id: 'm1', conversation_id: CONV, body: 'hi', created_at: '2026-01-02T00:00:00Z' },
    });
    const d = readMessages(qc);
    expect(d?.pages[0]?.data?.map((m) => m.id)).toEqual(['m1', 'm0']);
  });

  test('is a no-op when the message id is already cached (own-send echo)', () => {
    const qc = newClient();
    seedMessages(qc, [{ data: [{ id: 'm1', body: 'hi', created_at: '2026-01-02T00:00:00Z' }] }]);
    const before = readMessages(qc);
    applyWSEvent(qc, {
      type: 'message.new',
      data: { id: 'm1', conversation_id: CONV, body: 'hi', created_at: '2026-01-02T00:00:00Z' },
    });
    // Same object reference back → React Query won't re-render.
    expect(readMessages(qc)).toBe(before);
  });

  test('bumps the conversation row and re-sorts the list', () => {
    const qc = newClient();
    qc.setQueryData<ConversationList>(conversationsKey, {
      data: [
        { id: 'conv-0', last_message_at: '2026-01-05T00:00:00Z' },
        { id: CONV, last_message_at: '2026-01-01T00:00:00Z' },
      ],
    });
    applyWSEvent(qc, {
      type: 'message.new',
      data: { id: 'm1', conversation_id: CONV, body: 'hi', created_at: '2026-01-09T00:00:00Z' },
    });
    const list = qc.getQueryData<ConversationList>(conversationsKey);
    expect(list?.data?.map((c) => c.id)).toEqual([CONV, 'conv-0']);
    expect(list?.data?.[0]?.last_message_at).toBe('2026-01-09T00:00:00Z');
  });

  test('keeps pinned rows above unpinned after a bump', () => {
    const qc = newClient();
    qc.setQueryData<ConversationList>(conversationsKey, {
      data: [
        {
          id: 'pinned',
          pinned_at: '2026-01-01T00:00:00Z',
          last_message_at: '2026-01-02T00:00:00Z',
        },
        { id: CONV, last_message_at: '2026-01-01T00:00:00Z' },
      ],
    });
    applyWSEvent(qc, {
      type: 'message.new',
      data: { id: 'm1', conversation_id: CONV, body: 'hi', created_at: '2026-01-09T00:00:00Z' },
    });
    const list = qc.getQueryData<ConversationList>(conversationsKey);
    expect(list?.data?.map((c) => c.id)).toEqual(['pinned', CONV]);
  });

  test('ignores a payload missing conversation_id or id', () => {
    const qc = newClient();
    seedMessages(qc, [{ data: [{ id: 'm0' }] }]);
    const before = readMessages(qc);
    applyWSEvent(qc, { type: 'message.new', data: { id: 'm1' } });
    applyWSEvent(qc, { type: 'message.new', data: { conversation_id: CONV } });
    applyWSEvent(qc, { type: 'message.new' });
    expect(readMessages(qc)).toBe(before);
  });
});

describe('applyWSEvent — message.edited', () => {
  test('patches body + edited_at in place', () => {
    const qc = newClient();
    seedMessages(qc, [
      {
        data: [
          { id: 'm1', body: 'orig' },
          { id: 'm2', body: 'other' },
        ],
      },
    ]);
    applyWSEvent(qc, {
      type: 'message.edited',
      data: { id: 'm1', conversation_id: CONV, body: 'edited', edited_at: '2026-02-01T00:00:00Z' },
    });
    const rows = readMessages(qc)?.pages[0]?.data;
    expect(rows?.find((m) => m.id === 'm1')).toMatchObject({
      body: 'edited',
      edited_at: '2026-02-01T00:00:00Z',
    });
    expect(rows?.find((m) => m.id === 'm2')).toMatchObject({ body: 'other' });
  });
});

describe('applyWSEvent — message.deleted', () => {
  test('marks the row deleted and blanks the body', () => {
    const qc = newClient();
    seedMessages(qc, [{ data: [{ id: 'm1', body: 'secret', is_deleted: false }] }]);
    applyWSEvent(qc, {
      type: 'message.deleted',
      data: { message_id: 'm1', conversation_id: CONV },
    });
    expect(readMessages(qc)?.pages[0]?.data?.[0]).toMatchObject({
      id: 'm1',
      is_deleted: true,
      body: '',
    });
  });
});

describe('applyWSEvent — presence.update', () => {
  test('patches the friend presence row', () => {
    const qc = newClient();
    qc.setQueryData<PresenceList>(presenceKey, {
      data: [
        { user_id: 'u1', status: 'offline' },
        { user_id: 'u2', status: 'online' },
      ],
    });
    applyWSEvent(qc, {
      type: 'presence.update',
      data: { user_id: 'u1', status: 'online', last_active_at: '2026-03-01T00:00:00Z' },
    });
    const rows = qc.getQueryData<PresenceList>(presenceKey)?.data;
    expect(rows?.find((p) => p.user_id === 'u1')).toMatchObject({
      status: 'online',
      last_active_at: '2026-03-01T00:00:00Z',
    });
    expect(rows?.find((p) => p.user_id === 'u2')).toMatchObject({ status: 'online' });
  });
});

describe('applyWSEvent — friend.*', () => {
  test('friend.request_received invalidates the requests query', async () => {
    const qc = newClient();
    await qc.prefetchQuery({ queryKey: ['/v1/friends/requests'], queryFn: () => ({ data: [] }) });
    applyWSEvent(qc, { type: 'friend.request_received' });
    expect(qc.getQueryState(['/v1/friends/requests'])?.isInvalidated).toBe(true);
  });

  test('friend.request_accepted invalidates the friends + requests queries', async () => {
    const qc = newClient();
    await qc.prefetchQuery({ queryKey: ['/v1/friends'], queryFn: () => ({ data: [] }) });
    await qc.prefetchQuery({ queryKey: ['/v1/friends/requests'], queryFn: () => ({ data: [] }) });
    applyWSEvent(qc, { type: 'friend.request_accepted' });
    expect(qc.getQueryState(['/v1/friends'])?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['/v1/friends/requests'])?.isInvalidated).toBe(true);
  });
});

describe('applyWSEvent — conversation.*', () => {
  test('member_added invalidates the conversations list', async () => {
    const qc = newClient();
    await qc.prefetchQuery({ queryKey: ['/v1/conversations'], queryFn: () => ({ data: [] }) });
    applyWSEvent(qc, { type: 'conversation.member_added', data: { conversation_id: CONV } });
    expect(qc.getQueryState(['/v1/conversations'])?.isInvalidated).toBe(true);
  });
});

describe('applyWSEvent — deliberate no-ops', () => {
  test('room.* / typing.* / message.read / notification.new leave the cache untouched', () => {
    const qc = newClient();
    seedMessages(qc, [{ data: [{ id: 'm0', body: 'x' }] }]);
    const before = readMessages(qc);
    for (const type of [
      'room.started',
      'room.participant_joined',
      'room.participant_left',
      'room.video_changed',
      'room.ended',
      'typing.start',
      'typing.stop',
      'message.read',
      'notification.new',
    ]) {
      applyWSEvent(qc, { type, data: { conversation_id: CONV } });
    }
    expect(readMessages(qc)).toBe(before);
  });
});
