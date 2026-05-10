// Trailing affordance for any user row that wants to surface the
// caller's friendship state with that user. Same shape across the
// global search modal and the friends tab so users see one
// vocabulary in both places:
//
//   - friend          → null (no trailing — row tap opens the DM)
//   - none            → primary "Add friend" button
//   - outgoing        → outline "Unsend" button
//   - incoming        → variant-driven:
//       - "actions"   → outline ✕ + primary ✓ buttons (friends tab)
//       - "hint"      → muted text "Sent you a request" (search modal)
//
// Send + cancel are passed in (typically from useFriendActions) so
// callers can reuse the same hook's pending state to disable the
// right pill while a mutation flight is in progress.
import * as React from 'react';
import { View } from 'react-native';
import { Check, X } from 'lucide-react-native';

import { Button } from '@/components/ui/button';
import { Text } from '@/components/ui/text';
import { useThemeColor } from '@/lib/theme/use-theme-color';

export type FriendStatus =
  | { kind: 'friend' }
  | { kind: 'outgoing'; requestId: string }
  | { kind: 'incoming'; requestId: string };

type Props = {
  status: FriendStatus | undefined;
  username: string | undefined;
  /** Disabled / opening preempts everything (e.g. DM is being created). */
  busyLabel?: string;
  onAdd: (username: string) => void;
  onCancel: (requestId: string) => void;
  /** Per-row pending flags so only the tapped pill spins. */
  isAdding: boolean;
  isCanceling: boolean;
  /** Optional accept/decline for incoming requests; only used in
   * `incomingMode: 'actions'`. */
  onAccept?: (requestId: string) => void;
  onDecline?: (requestId: string) => void;
  acceptDisabled?: boolean;
  /** How to render an incoming pending request:
   *   - 'actions': two icon buttons (Accept / Decline) — friends tab
   *   - 'hint':    dim "Sent you a request" text — search modal */
  incomingMode?: 'actions' | 'hint';
  testID?: string;
};

export function FriendStatusAction({
  status,
  username,
  busyLabel,
  onAdd,
  onCancel,
  isAdding,
  isCanceling,
  onAccept,
  onDecline,
  acceptDisabled,
  incomingMode = 'hint',
  testID,
}: Props) {
  const fg = useThemeColor('foreground');

  if (busyLabel) {
    return (
      <Text variant="muted" className="text-xs">
        {busyLabel}
      </Text>
    );
  }
  if (status?.kind === 'friend') return null;

  if (status?.kind === 'incoming') {
    if (incomingMode === 'actions' && onAccept && onDecline) {
      return (
        <View className="flex-row items-center gap-2">
          <Button
            size="icon"
            variant="outline"
            disabled={!!acceptDisabled}
            onPress={() => onDecline(status.requestId)}
            accessibilityLabel="Decline friend request"
            testID={testID ? `${testID}-decline` : 'friend-action-decline'}>
            <X size={16} color={fg} />
          </Button>
          <Button
            size="icon"
            variant="default"
            disabled={!!acceptDisabled}
            onPress={() => onAccept(status.requestId)}
            accessibilityLabel="Accept friend request"
            testID={testID ? `${testID}-accept` : 'friend-action-accept'}>
            <Check size={16} color="#fff" />
          </Button>
        </View>
      );
    }
    return (
      <Text variant="muted" className="text-xs">
        Sent you a request
      </Text>
    );
  }

  if (status?.kind === 'outgoing') {
    return (
      <Button
        size="sm"
        variant="outline"
        disabled={isCanceling}
        onPress={() => onCancel(status.requestId)}
        accessibilityLabel="Unsend friend request"
        testID={testID ? `${testID}-unsend` : 'friend-action-unsend'}>
        <Text>{isCanceling ? 'Unsending…' : 'Unsend'}</Text>
      </Button>
    );
  }

  // No relationship — Add friend pill.
  if (!username) return null;
  return (
    <Button
      size="sm"
      variant="default"
      disabled={isAdding}
      onPress={() => onAdd(username)}
      accessibilityLabel={`Send friend request to ${username}`}
      testID={testID ? `${testID}-add` : 'friend-action-add'}>
      <Text>{isAdding ? 'Adding…' : 'Add friend'}</Text>
    </Button>
  );
}
