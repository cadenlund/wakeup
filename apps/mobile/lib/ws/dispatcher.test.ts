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
  InternalHandlerHttpPresenceListResponse,
} from '@/lib/api/model';
import { applyWSEvent } from '@/lib/ws/dispatcher';

type ConversationList = InternalHandlerHttpConversationListResponse;
type PresenceList = InternalHandlerHttpPresenceListResponse;

const CONV = 'conv-1';
const messagesKey = [`/v1/conversations/${CONV}/messages`, { limit: 20, q: undefined }, 'infinite'];
const conversationsKey = ['/v1/conversations'];
const presenceKey = ['/v1/presence/friends'];
const friendsKey = ['/v1/friends'];
const friendRequestsKey = ['/v1/friends/requests'];

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

// Seed a query so `invalidateQueries` has something to mark stale.
async function seedQuery(qc: QueryClient, key: readonly unknown[], data: unknown): Promise<void> {
  await qc.prefetchQuery({ queryKey: key, queryFn: () => data });
}
function isInvalidated(qc: QueryClient, key: readonly unknown[]): boolean {
  return qc.getQueryState(key)?.isInvalidated === true;
}

// Backend `publishMessageEvent` wire shape — ids only, no body.
function messageEvent(type: string, extra: Record<string, unknown> = {}) {
  return {
    type,
    data: {
      message_id: 'm1',
      conversation_id: CONV,
      sender_id: 'u9',
      created_at: '2026-01-09T00:00:00Z',
      ...extra,
    },
  };
}

describe('applyWSEvent — message.new', () => {
  test('invalidates the thread messages query', async () => {
    const qc = newClient();
    await seedQuery(qc, messagesKey, { pages: [{ data: [] }], pageParams: [undefined] });
    applyWSEvent(qc, messageEvent('message.new'));
    expect(isInvalidated(qc, messagesKey)).toBe(true);
  });

  test('bumps the conversation row by created_at and re-sorts', () => {
    const qc = newClient();
    qc.setQueryData<ConversationList>(conversationsKey, {
      data: [
        { id: 'conv-0', last_message_at: '2026-01-05T00:00:00Z' },
        { id: CONV, last_message_at: '2026-01-01T00:00:00Z' },
      ],
    });
    applyWSEvent(qc, messageEvent('message.new'));
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
    applyWSEvent(qc, messageEvent('message.new'));
    expect(qc.getQueryData<ConversationList>(conversationsKey)?.data?.map((c) => c.id)).toEqual([
      'pinned',
      CONV,
    ]);
  });

  test('ignores a payload missing conversation_id', () => {
    const qc = newClient();
    qc.setQueryData<ConversationList>(conversationsKey, { data: [{ id: CONV }] });
    const before = qc.getQueryData<ConversationList>(conversationsKey);
    applyWSEvent(qc, { type: 'message.new', data: { message_id: 'm1' } });
    applyWSEvent(qc, { type: 'message.new' });
    expect(qc.getQueryData<ConversationList>(conversationsKey)).toBe(before);
  });
});

describe('applyWSEvent — message.edited / message.deleted', () => {
  test('both invalidate the thread messages query', async () => {
    for (const type of ['message.edited', 'message.deleted']) {
      const qc = newClient();
      await seedQuery(qc, messagesKey, { pages: [{ data: [] }], pageParams: [undefined] });
      applyWSEvent(qc, messageEvent(type));
      expect(isInvalidated(qc, messagesKey)).toBe(true);
    }
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

  test('a payload with only user_id leaves the row untouched (no undefined merge)', () => {
    const qc = newClient();
    qc.setQueryData<PresenceList>(presenceKey, { data: [{ user_id: 'u1', status: 'offline' }] });
    const before = qc.getQueryData<PresenceList>(presenceKey);
    applyWSEvent(qc, { type: 'presence.update', data: { user_id: 'u1' } });
    applyWSEvent(qc, { type: 'presence.update', data: { user_id: 'u1', status: 42 } });
    expect(qc.getQueryData<PresenceList>(presenceKey)).toBe(before);
  });
});

describe('applyWSEvent — friend.*', () => {
  test('friend.request_received invalidates the requests query', async () => {
    const qc = newClient();
    await seedQuery(qc, friendRequestsKey, { data: [] });
    applyWSEvent(qc, { type: 'friend.request_received' });
    expect(isInvalidated(qc, friendRequestsKey)).toBe(true);
  });

  test('friend.request_accepted invalidates the friends + requests queries', async () => {
    const qc = newClient();
    await seedQuery(qc, friendsKey, { data: [] });
    await seedQuery(qc, friendRequestsKey, { data: [] });
    applyWSEvent(qc, { type: 'friend.request_accepted' });
    expect(isInvalidated(qc, friendsKey)).toBe(true);
    expect(isInvalidated(qc, friendRequestsKey)).toBe(true);
  });
});

describe('applyWSEvent — conversation.*', () => {
  test('member_added invalidates the conversations list', async () => {
    const qc = newClient();
    await seedQuery(qc, conversationsKey, { data: [] });
    applyWSEvent(qc, { type: 'conversation.member_added', data: { conversation_id: CONV } });
    expect(isInvalidated(qc, conversationsKey)).toBe(true);
  });
});

describe('applyWSEvent — deliberate no-ops', () => {
  test('room.* / typing.* / message.read / notification.new touch nothing', async () => {
    const qc = newClient();
    await seedQuery(qc, messagesKey, { pages: [{ data: [] }], pageParams: [undefined] });
    await seedQuery(qc, conversationsKey, { data: [] });
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
    expect(isInvalidated(qc, messagesKey)).toBe(false);
    expect(isInvalidated(qc, conversationsKey)).toBe(false);
  });
});
