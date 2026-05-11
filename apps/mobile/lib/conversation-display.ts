// Shared "what does this conversation row look like" helper.
//
// Used by the chats tab and the global /search modal so a
// conversation that renders in both places looks identical —
// same title rules, same group fallback (stacked-avatars when no
// avatar_url is set, comma-joined member preview when no name is
// set), same presence dots, same "N members" subtitle. Without
// the shared helper the search modal had to render a slim
// avatar/name cell because it didn't have access to the
// chats-tab cache.
import type {
  InternalHandlerHttpConversationResponse,
  InternalHandlerHttpUserResponse,
} from '@/lib/api/model';

export type ConversationDisplay = {
  title: string;
  subtitle?: string;
  avatarUrl?: string | null;
  fallbackInitial?: string;
  // Two member avatars to render in a stacked cluster when the
  // group has no avatar_url. Each carries its own presence so the
  // cluster can show two dots. Empty / undefined for direct convos.
  stackedMembers?: {
    avatarUrl?: string | null;
    fallbackName?: string | null;
    presence?: string | null;
  }[];
  // Presence to overlay on the (single) avatar. Set for direct DMs
  // where there's a clear "the other person"; unset for groups
  // where per-member dots ride on stackedMembers instead.
  presence?: string | null;
};

export function conversationDisplay(
  c: InternalHandlerHttpConversationResponse,
  myUserId: string | undefined,
  presenceByUser: Map<string, string>
): ConversationDisplay {
  if (c.type === 'direct') {
    // For a 1:1 conversation, we want the *other* member. Server may
    // include the caller as a member; filter them out so a self-DM
    // (rare; admin tooling) at least falls back to the same row.
    // When myUserId is unknown (cold load), picking the first
    // member is acceptable — direct convos only have two so any
    // member is a reasonable identity for the row.
    const others = myUserId
      ? (c.members ?? []).filter((m) => m.user?.id && m.user.id !== myUserId)
      : (c.members ?? []);
    const other = others[0]?.user ?? c.members?.[0]?.user;
    const title = other?.display_name?.trim() || other?.username?.trim() || 'Direct message';
    return {
      title,
      avatarUrl: other?.avatar_url,
      fallbackInitial: title,
      presence: other?.id ? (presenceByUser.get(other.id) ?? null) : null,
    };
  }
  // group
  const others = myUserId
    ? (c.members ?? []).filter((m) => m.user?.id && m.user.id !== myUserId)
    : (c.members ?? []);
  const memberCount = (c.members ?? []).length;
  const stackedMembers = others.slice(0, 2).map((m) => ({
    avatarUrl: m.user?.avatar_url,
    fallbackName: m.user?.display_name ?? m.user?.username ?? null,
    presence: m.user?.id ? (presenceByUser.get(m.user.id) ?? null) : null,
  }));

  const named = c.name?.trim();
  if (named) {
    // Named group → subtitle is "N members" so the avatar / name +
    // count read as a complete identity even before any messages.
    const subtitle = memberCount > 0 ? membersLabel(memberCount) : undefined;
    return {
      title: named,
      subtitle,
      avatarUrl: c.avatar_url,
      fallbackInitial: named,
      stackedMembers,
    };
  }
  // Unnamed group — fall back to a comma-joined preview of up to
  // three member names with an "and N more" overflow indicator so
  // the row reads as "you + these people" at a glance. Subtitle
  // becomes the same "N members" line a named group uses, so the
  // two shapes feel consistent.
  const previewNames = others
    .map((m) => m.user?.display_name?.trim() || m.user?.username?.trim())
    .filter((s): s is string => !!s);
  const shownPreviewNames = previewNames.slice(0, 3);
  const remaining = previewNames.length - shownPreviewNames.length;
  const previewShown = shownPreviewNames.join(', ');
  const title = previewShown
    ? remaining > 0
      ? `${previewShown} and ${remaining} more`
      : previewShown
    : 'Group';
  const subtitle = memberCount > 0 ? membersLabel(memberCount) : undefined;
  return {
    title,
    subtitle,
    avatarUrl: c.avatar_url,
    fallbackInitial: previewShown || 'G',
    stackedMembers,
  };
}

export function isCurrentlyMuted(mutedUntil: string | null | undefined): boolean {
  if (!mutedUntil) return false;
  const t = Date.parse(mutedUntil);
  if (Number.isNaN(t)) return false;
  return t > Date.now();
}

export function membersLabel(n: number): string {
  return n === 1 ? '1 member' : `${n} members`;
}

// Search filter for the chats-tab inline filter bar. Returns
// conversations whose title (direct: other member's
// display_name/username; group: name) or any member's identity
// matches the term. Includes self in the member walk because users
// occasionally search by their own name in a group ("am I in this
// chat?"); harmless and removes a stale-myUserId failure mode.
export function filterConversations(
  rows: InternalHandlerHttpConversationResponse[],
  rawQuery: string
): InternalHandlerHttpConversationResponse[] {
  const term = rawQuery.trim().toLowerCase();
  if (!term) return rows;
  return rows.filter((c) => {
    const named = c.name?.toLowerCase() ?? '';
    if (named.includes(term)) return true;
    return (c.members ?? []).some((m) => {
      const dn = m.user?.display_name?.toLowerCase() ?? '';
      const un = m.user?.username?.toLowerCase() ?? '';
      return dn.includes(term) || un.includes(term);
    });
  });
}

// Helper for callers that have a "user" record (e.g. a friend
// from the friends list) but want to match the same shape used by
// presence + display.
export type DisplayUser = InternalHandlerHttpUserResponse;

// Display string for a single user, using the same fallback chain
// as the rest of the thread UI (display_name → username → generic
// label). Soft-deleted accounts arrive with a server-set
// display_name (e.g. "Deleted User"), which flows through here
// unchanged — callers never need their own deleted-user branch.
export function userDisplayName(u: DisplayUser | null | undefined, fallback = 'Someone'): string {
  return u?.display_name?.trim() || u?.username?.trim() || fallback;
}
