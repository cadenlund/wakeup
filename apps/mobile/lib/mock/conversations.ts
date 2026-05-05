// Mock conversation store — backs the Messages list (§5.1), the
// thread screen, the create-group modal, and the edit-members
// screen. Replaced by the real /v1/conversations + messages hooks
// at Phase 6 — keeping the surface (Conversation + Message + the
// action verbs) in the shape the production hooks will return so
// the swap is a one-file change.

import { create } from 'zustand';

import type { Friend } from '@/lib/mock/friends';

export type Presence = 'online' | 'away' | 'sleeping' | 'dnd' | 'offline';

type ConversationBase = {
  id: string;
  name: string;
  pinned?: boolean;
  muted?: boolean;
  unread?: number;
  lastTime: string;
  voiceRoom?: { participants: number };
  typing?: string[];
  preview: string;
};

export type DmConversation = ConversationBase & {
  kind: 'dm';
  initials: string;
  presence: Presence;
  statusEmoji?: string;
  bio?: string;
};

export type GroupMember = { id?: string; initials: string; color: string; name: string };

export type GroupConversation = ConversationBase & {
  kind: 'group';
  members: GroupMember[];
  emoji?: string;
};

export type Conversation = DmConversation | GroupConversation;

export type Message =
  | {
      id: string;
      kind: 'message';
      senderName: string;
      senderInitials: string;
      senderColor: string;
      mine: boolean;
      text: string;
      timestamp: string;
      status?: 'sent' | 'delivered' | 'read';
      reactions?: { emoji: string; count: number }[];
    }
  | {
      id: string;
      kind: 'system';
      text: string;
      timestamp: string;
    };

const SEED_CONVERSATIONS: Conversation[] = [
  {
    id: 'c1',
    kind: 'dm',
    name: 'Alice Chen',
    initials: 'AC',
    presence: 'online',
    statusEmoji: '🎧',
    bio: 'building the dream sleep mix',
    pinned: true,
    typing: ['Alice'],
    preview: 'is typing…',
    lastTime: 'now',
    unread: 2,
  },
  {
    id: 'c2',
    kind: 'group',
    name: 'Sleep Sketches',
    emoji: '🎶',
    members: [
      { initials: 'YT', color: '#a855f7', name: 'Yuki' },
      { initials: 'PP', color: '#22d3ee', name: 'Priya' },
      { initials: 'MR', color: '#f472b6', name: 'Marcus' },
    ],
    pinned: true,
    voiceRoom: { participants: 3 },
    preview: '🎤 Yuki: that snare is heaven',
    lastTime: '1m',
    unread: 7,
  },
  {
    id: 'c3',
    kind: 'dm',
    name: 'Marcus Reed',
    initials: 'MR',
    presence: 'online',
    statusEmoji: '☕',
    bio: 'caffeinated',
    preview: 'shipped 🎉 — review when you get a sec',
    lastTime: '2m',
  },
  {
    id: 'c4',
    kind: 'group',
    name: 'Late night devs',
    emoji: '🌙',
    members: [
      { initials: 'DR', color: '#34d399', name: 'Diego' },
      { initials: 'AC', color: '#fb923c', name: 'Alice' },
      { initials: 'SW', color: '#facc15', name: 'Sam' },
    ],
    typing: ['Diego'],
    preview: 'Diego is typing…',
    lastTime: '5m',
    unread: 1,
  },
  {
    id: 'c5',
    kind: 'dm',
    name: 'Priya Patel',
    initials: 'PP',
    presence: 'away',
    statusEmoji: '🚶',
    preview: 'ttyl 🚶',
    lastTime: '12m',
  },
  {
    id: 'c6',
    kind: 'dm',
    name: 'Yuki Tanaka',
    initials: 'YT',
    presence: 'sleeping',
    statusEmoji: '🌙',
    preview: 'good night 🌙',
    lastTime: '1h',
  },
  {
    id: 'c7',
    kind: 'group',
    name: 'Quiet Lab',
    emoji: '🔬',
    members: [
      { initials: 'OW', color: '#fb923c', name: 'Owen' },
      { initials: 'LB', color: '#a855f7', name: 'Lily' },
    ],
    muted: true,
    preview: 'Lily: ok pushing the patch',
    lastTime: '3h',
    unread: 4,
  },
  {
    id: 'c8',
    kind: 'dm',
    name: 'Diego Romero',
    initials: 'DR',
    presence: 'dnd',
    statusEmoji: '🧠',
    preview: '🧠 deep focus until 3pm',
    lastTime: 'Yesterday',
  },
];

const ME = { name: 'You', initials: 'CL', color: '#1e40af' };

const SEED_MESSAGES: Record<string, Message[]> = {
  c1: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Alice Chen',
      senderInitials: 'AC',
      senderColor: '#fb923c',
      mine: false,
      text: 'hey, did you catch the new sleep mix yuki posted?',
      timestamp: '12:42',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'not yet — saving it for tonight',
      timestamp: '12:45',
      status: 'read',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'thinking of opening a voice room around 9?',
      timestamp: '12:45',
      status: 'read',
    },
    {
      id: 'm4',
      kind: 'message',
      senderName: 'Alice Chen',
      senderInitials: 'AC',
      senderColor: '#fb923c',
      mine: false,
      text: '🎧 yes please',
      timestamp: '12:50',
      reactions: [{ emoji: '🔥', count: 1 }],
    },
    {
      id: 'm5',
      kind: 'message',
      senderName: 'Alice Chen',
      senderInitials: 'AC',
      senderColor: '#fb923c',
      mine: false,
      text: 'i was actually about to ask you the same thing',
      timestamp: '12:51',
    },
  ],
  c2: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Yuki',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'fresh demo just dropped 🎶',
      timestamp: 'Yesterday',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: 'Yuki',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'https://wakeup.app/listen/sleep-sketches-04',
      timestamp: 'Yesterday',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: 'Marcus',
      senderInitials: 'MR',
      senderColor: '#f472b6',
      mine: false,
      text: 'this hits HARD',
      timestamp: '10:00',
      reactions: [
        { emoji: '🔥', count: 3 },
        { emoji: '🎧', count: 2 },
      ],
    },
    {
      id: 'm4',
      kind: 'message',
      senderName: 'Priya',
      senderInitials: 'PP',
      senderColor: '#22d3ee',
      mine: false,
      text: 'joining voice',
      timestamp: '10:02',
    },
    {
      id: 'm5',
      kind: 'system',
      text: 'Yuki started a voice room',
      timestamp: '10:05',
    },
    {
      id: 'm6',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'be there in 2',
      timestamp: '10:06',
      status: 'read',
    },
    {
      id: 'm7',
      kind: 'message',
      senderName: 'Yuki',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'tweak the snare mid-bar?',
      timestamp: '10:30',
    },
    {
      id: 'm8',
      kind: 'message',
      senderName: 'Marcus',
      senderInitials: 'MR',
      senderColor: '#f472b6',
      mine: false,
      text: "yeah it's hot but maybe pull it back 1db",
      timestamp: '10:31',
    },
    {
      id: 'm9',
      kind: 'message',
      senderName: 'Yuki',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'that snare is heaven 😮‍💨',
      timestamp: 'now',
      reactions: [{ emoji: '✨', count: 1 }],
    },
  ],
  c3: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Marcus Reed',
      senderInitials: 'MR',
      senderColor: '#f472b6',
      mine: false,
      text: 'PR is up — friend graph search index',
      timestamp: 'Yesterday',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: '👀 looking now',
      timestamp: 'Yesterday',
      status: 'read',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'lgtm — left a couple of nits',
      timestamp: '12:30',
      status: 'read',
    },
    {
      id: 'm4',
      kind: 'message',
      senderName: 'Marcus Reed',
      senderInitials: 'MR',
      senderColor: '#f472b6',
      mine: false,
      text: 'shipped 🎉 — review when you get a sec',
      timestamp: '12:30',
      reactions: [{ emoji: '🎉', count: 1 }],
    },
  ],
  c4: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Sam',
      senderInitials: 'SW',
      senderColor: '#facc15',
      mine: false,
      text: 'anyone seen the build break on main?',
      timestamp: '1h',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: 'Alice',
      senderInitials: 'AC',
      senderColor: '#fb923c',
      mine: false,
      text: 'yeah pulling now',
      timestamp: '1h',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: 'Alice',
      senderInitials: 'AC',
      senderColor: '#fb923c',
      mine: false,
      text: "looks like it's the websocket reconnect test",
      timestamp: '1h',
    },
  ],
  c5: [
    {
      id: 'm1',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'wanna grab coffee tmrw?',
      timestamp: '12m',
      status: 'delivered',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: 'Priya Patel',
      senderInitials: 'PP',
      senderColor: '#22d3ee',
      mine: false,
      text: 'ttyl 🚶',
      timestamp: '12m',
    },
  ],
  c6: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Yuki Tanaka',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'sleep tight!',
      timestamp: 'Yesterday',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'rest up 🌙',
      timestamp: '1h',
      status: 'read',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: 'Yuki Tanaka',
      senderInitials: 'YT',
      senderColor: '#a855f7',
      mine: false,
      text: 'good night 🌙',
      timestamp: '1h',
    },
  ],
  c7: [
    {
      id: 'm1',
      kind: 'message',
      senderName: 'Owen',
      senderInitials: 'OW',
      senderColor: '#fb923c',
      mine: false,
      text: 'patch is ready in #ops',
      timestamp: '3h',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: 'Lily',
      senderInitials: 'LB',
      senderColor: '#a855f7',
      mine: false,
      text: 'ok pushing the patch',
      timestamp: '3h',
    },
    {
      id: 'm3',
      kind: 'message',
      senderName: 'Lily',
      senderInitials: 'LB',
      senderColor: '#a855f7',
      mine: false,
      text: 'should be live in 5',
      timestamp: '3h',
    },
    {
      id: 'm4',
      kind: 'message',
      senderName: 'Lily',
      senderInitials: 'LB',
      senderColor: '#a855f7',
      mine: false,
      text: 'live ✅',
      timestamp: '3h',
      reactions: [{ emoji: '🚀', count: 2 }],
    },
  ],
  c8: [
    {
      id: 'm1',
      kind: 'message',
      senderName: ME.name,
      senderInitials: ME.initials,
      senderColor: ME.color,
      mine: true,
      text: 'ping me when you have a sec',
      timestamp: 'Yesterday',
      status: 'read',
    },
    {
      id: 'm2',
      kind: 'message',
      senderName: 'Diego Romero',
      senderInitials: 'DR',
      senderColor: '#34d399',
      mine: false,
      text: '🧠 deep focus until 3pm',
      timestamp: 'Yesterday',
    },
  ],
};

function friendToMember(f: Friend): GroupMember {
  // Stable per-name palette so newly added members render with the
  // same colour they already had elsewhere in the app.
  const palette = ['#fb923c', '#a855f7', '#22d3ee', '#f472b6', '#34d399', '#facc15'];
  let h = 0;
  for (let i = 0; i < f.name.length; i++) h = (h * 31 + f.name.charCodeAt(i)) >>> 0;
  return {
    id: f.id,
    initials: f.initials,
    color: palette[h % palette.length] as string,
    name: f.name,
  };
}

type ConversationsState = {
  conversations: Conversation[];
  messagesByConversation: Record<string, Message[]>;
  togglePin: (id: string) => void;
  toggleMute: (id: string) => void;
  deleteConversation: (id: string) => void;
  // Returns the new group's id so the caller can navigate to it.
  createGroup: (input: { name: string; emoji?: string; members: Friend[] }) => string;
  addMembers: (id: string, members: Friend[]) => void;
  removeMember: (id: string, memberId: string) => void;
};

export const useConversationsStore = create<ConversationsState>((set, get) => ({
  conversations: SEED_CONVERSATIONS,
  messagesByConversation: SEED_MESSAGES,

  togglePin: (id) =>
    set((s) => ({
      conversations: s.conversations.map((c) =>
        c.id === id ? { ...c, pinned: !c.pinned } : c,
      ),
    })),

  toggleMute: (id) =>
    set((s) => ({
      conversations: s.conversations.map((c) => (c.id === id ? { ...c, muted: !c.muted } : c)),
    })),

  deleteConversation: (id) =>
    set((s) => {
      const next = { ...s.messagesByConversation };
      delete next[id];
      return {
        conversations: s.conversations.filter((c) => c.id !== id),
        messagesByConversation: next,
      };
    }),

  createGroup: ({ name, emoji, members }) => {
    const id = `g-${Date.now()}`;
    const groupMembers = members.map(friendToMember);
    const previewMembers = groupMembers
      .slice(0, 3)
      .map((m) => m.name)
      .join(', ');
    const newGroup: GroupConversation = {
      id,
      kind: 'group',
      name: name.trim() || 'New group',
      emoji,
      members: groupMembers,
      preview: `New group · ${previewMembers}${groupMembers.length > 3 ? `, +${groupMembers.length - 3}` : ''}`,
      lastTime: 'now',
    };
    set((s) => ({
      conversations: [newGroup, ...s.conversations],
      messagesByConversation: {
        ...s.messagesByConversation,
        [id]: [
          {
            id: 'm1',
            kind: 'system',
            text: `You created the group with ${groupMembers.map((m) => m.name).join(', ')}.`,
            timestamp: 'now',
          },
        ],
      },
    }));
    return id;
  },

  addMembers: (id, friends) =>
    set((s) => {
      const target = s.conversations.find((c) => c.id === id);
      if (!target || target.kind !== 'group') return s;
      const existingIds = new Set(
        target.members.map((m) => m.id).filter((m): m is string => Boolean(m)),
      );
      const fresh = friends.filter((f) => !existingIds.has(f.id)).map(friendToMember);
      if (fresh.length === 0) return s;
      const updated: GroupConversation = {
        ...target,
        members: [...target.members, ...fresh],
      };
      const newSystem: Message = {
        id: `m-${Date.now()}`,
        kind: 'system',
        text: `Added ${fresh.map((f) => f.name).join(', ')} to the group.`,
        timestamp: 'now',
      };
      return {
        conversations: s.conversations.map((c) => (c.id === id ? updated : c)),
        messagesByConversation: {
          ...s.messagesByConversation,
          [id]: [...(s.messagesByConversation[id] ?? []), newSystem],
        },
      };
    }),

  removeMember: (id, memberId) =>
    set((s) => {
      const target = s.conversations.find((c) => c.id === id);
      if (!target || target.kind !== 'group') return s;
      const removed = target.members.find((m) => m.id === memberId);
      const updated: GroupConversation = {
        ...target,
        members: target.members.filter((m) => m.id !== memberId),
      };
      const newSystem: Message = {
        id: `m-${Date.now()}`,
        kind: 'system',
        text: removed
          ? `Removed ${removed.name} from the group.`
          : 'Removed a member from the group.',
        timestamp: 'now',
      };
      return {
        conversations: s.conversations.map((c) => (c.id === id ? updated : c)),
        messagesByConversation: {
          ...s.messagesByConversation,
          [id]: [...(s.messagesByConversation[id] ?? []), newSystem],
        },
      };
    }),
}));

// Selectors / helpers — keep the same shape the screens consumed
// before the Zustand refactor so existing callers don't all need
// rewiring.
export function getConversationById(id: string): Conversation | undefined {
  return useConversationsStore.getState().conversations.find((c) => c.id === id);
}

export function getMessagesForConversation(id: string): Message[] {
  return useConversationsStore.getState().messagesByConversation[id] ?? [];
}
