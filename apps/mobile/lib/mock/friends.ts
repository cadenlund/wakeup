// Mock friends store — backs the Friends tab and the invite modal
// during the Phase 1.4 preview. Replaced by the real
// /v1/friends + /v1/friends/{id}/* hooks at Phase 7. The shape
// (Friend + the action verbs) tracks what the production hooks will
// expose, so the swap is a one-file change at that point.

import { create } from 'zustand';

export type Presence = 'online' | 'away' | 'sleeping' | 'dnd' | 'offline';

export type Friend = {
  id: string;
  name: string;
  initials: string;
  presence: Presence;
  statusEmoji?: string;
  status?: string;
  unread?: number;
  isFavorite?: boolean;
  isMuted?: boolean;
};

const SEED: Friend[] = [
  {
    id: '1',
    name: 'Alice Chen',
    initials: 'AC',
    presence: 'online',
    statusEmoji: '🎧',
    status: 'deep focus',
    unread: 2,
    isFavorite: true,
  },
  {
    id: '2',
    name: 'Marcus Reed',
    initials: 'MR',
    presence: 'online',
    statusEmoji: '☕',
    status: 'caffeinated',
  },
  {
    id: '3',
    name: 'Priya Patel',
    initials: 'PP',
    presence: 'away',
    statusEmoji: '🚶',
    status: 'on a walk',
  },
  {
    id: '4',
    name: 'Diego Romero',
    initials: 'DR',
    presence: 'dnd',
    statusEmoji: '🧠',
    status: 'do not disturb',
  },
  {
    id: '5',
    name: 'Yuki Tanaka',
    initials: 'YT',
    presence: 'sleeping',
    statusEmoji: '🌙',
    status: 'good night',
    isFavorite: true,
  },
  {
    id: '6',
    name: 'Sam Whitfield',
    initials: 'SW',
    presence: 'offline',
    status: 'last seen 3h ago',
  },
];

type FriendsState = {
  friends: Friend[];
  toggleFavorite: (id: string) => void;
  toggleMute: (id: string) => void;
  removeFriend: (id: string) => void;
  blockFriend: (id: string) => void;
  // Mock invite — just appends a new "online" friend with the given
  // identifier as a stand-in for the backend's "I sent the request,
  // they accepted, here's the row" flow.
  acceptInvite: (handle: string) => void;
};

function makeInitials(name: string): string {
  const parts = name.trim().split(/\s+/);
  if (parts.length === 0) return '··';
  if (parts.length === 1) return (parts[0]?.slice(0, 2) ?? '··').toUpperCase();
  return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase();
}

export const useFriendsStore = create<FriendsState>((set) => ({
  friends: SEED,
  toggleFavorite: (id) =>
    set((s) => ({
      friends: s.friends.map((f) =>
        f.id === id ? { ...f, isFavorite: !f.isFavorite } : f,
      ),
    })),
  toggleMute: (id) =>
    set((s) => ({
      friends: s.friends.map((f) => (f.id === id ? { ...f, isMuted: !f.isMuted } : f)),
    })),
  removeFriend: (id) =>
    set((s) => ({ friends: s.friends.filter((f) => f.id !== id) })),
  blockFriend: (id) =>
    // Block is a hard remove from the friends list in the mock; the
    // real backend will keep them in a separate /v1/blocks list.
    set((s) => ({ friends: s.friends.filter((f) => f.id !== id) })),
  acceptInvite: (handle) =>
    set((s) => {
      const cleaned = handle.replace(/^@/, '').trim();
      if (!cleaned) return s;
      const looksLikeEmail = cleaned.includes('@');
      const display = looksLikeEmail ? cleaned.split('@')[0]! : cleaned;
      const id = `invited-${Date.now()}`;
      return {
        friends: [
          ...s.friends,
          {
            id,
            name: display
              .split(/[._-]/)
              .filter(Boolean)
              .map((p) => p[0]!.toUpperCase() + p.slice(1))
              .join(' ') || display,
            initials: makeInitials(display),
            presence: 'online',
            status: 'just joined',
          },
        ],
      };
    }),
}));
