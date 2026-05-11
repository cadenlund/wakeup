// Message action popover — the iMessage "lift" interaction.
//
// On long-press the bubble's window rect is measured and handed
// in here. We render a full-screen transparent Modal that:
//   - dims everything else (dark scrim) so the focus lands on the
//     long-pressed message + this menu — nothing else;
//   - pins a non-interactive snapshot of the bubble at its
//     original position, scaled up a touch so it reads as "lifted
//     off" the thread;
//   - floats a compact, icon-only action pill that *rests just
//     above* the bubble (flips below only when the bubble sits too
//     near the top edge), aligned to the bubble's side — same
//     rounded-pill, elevated-surface look as the chat itself, with
//     a spring scale-in so it feels native.
//
// Actions (icon-only): Copy / React (v2 stub) / Report (no
// moderation backend yet — neutral acknowledgement; hidden on your
// own messages) / Delete (own non-deleted messages only).
import * as Clipboard from 'expo-clipboard';
import { Copy, Flag, SmilePlus, Trash2 } from 'lucide-react-native';
import * as React from 'react';
import { Animated, Modal, Platform, Pressable, useWindowDimensions, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { Text } from '@/components/ui/text';
import { formatTimeDividerLabel } from '@/components/time-divider';
import { toast } from '@/lib/toast';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { cn } from '@/lib/utils';

// Window-coordinate rect of the long-pressed bubble.
export type BubbleRect = { x: number; y: number; width: number; height: number };

// The bubble the popover was opened on. `mine` gates Delete +
// hides Report; `isDeleted` hides Copy. `body` is what Copy writes
// + what the snapshot renders. `createdAt` drives the "Sent …"
// caption. `rect` anchors the pill; when it's undefined the pill
// centers itself.
export type MessageActionTarget = {
  id: string;
  body: string;
  mine: boolean;
  isDeleted: boolean;
  createdAt: string | null | undefined;
  rect: BubbleRect | undefined;
};

type Props = {
  target: MessageActionTarget | null;
  onClose: () => void;
  // Caller owns the optimistic cache patch + the DELETE call.
  onDelete: (messageId: string) => void;
  testID?: string;
};

// Icon button geometry inside the card.
const ICON_BTN = 44; // touch target — keep ≥44
const ICON_ROW_PAD = 2; // horizontal padding flanking the icon row
const CAPTION_PAD = 10; // horizontal padding flanking the timestamp row
// Rough width-per-char for the timestamp string at the 11px
// caption font. Timestamps are mostly digits + a few letters +
// spaces, so a flat estimate is good enough; over-estimate a hair
// so the caption never truncates. We can't measure text before
// layout, so this drives the card width.
const CAPTION_CHAR_PX = 6.6;
const GAP = 8; // breathing room between the card and the bubble
const EDGE_PAD = 12; // keep everything this far from the screen edges
const SNAPSHOT_SCALE = 1.04;
// Rendered-height estimates used to place the card (it's
// absolutely-positioned, so we can't measure it before laying it
// out). Kept close to the real CSS so the card sits the intended
// GAP from the bubble — overestimating pushes it visibly too far.
const CAPTION_ROW_H = 26; // px + ~14px text + 1px border
const ICON_ROW_H = ICON_BTN; // the icon row's height is just the buttons
// Snapshot bubble caps its width like the in-thread bubble's
// `max-w-[80%]` so short text ("Message deleted") doesn't get
// re-wrapped narrow and tall. Slightly looser than 80% so it can
// only ever wrap *less* than the original, never more.
const SNAPSHOT_MAX_WIDTH_RATIO = 0.82;

type Action = {
  key: string;
  label: string;
  icon: React.ReactNode;
  onPress: () => void;
  destructive?: boolean;
};

export function MessageActionPopover({ target, onClose, onDelete, testID }: Props) {
  // Inner component so the spring-in animation runs on every open
  // (its mount lifecycle == the popover's open lifecycle).
  if (!target) return null;
  return <PopoverContent target={target} onClose={onClose} onDelete={onDelete} testID={testID} />;
}

function PopoverContent({
  target,
  onClose,
  onDelete,
  testID,
}: {
  target: MessageActionTarget;
  onClose: () => void;
  onDelete: (messageId: string) => void;
  testID?: string;
}) {
  const { width: screenW, height: screenH } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const fg = useThemeColor('foreground');
  const destructive = useThemeColor('destructive');
  const cardBg = useThemeColor('card');
  const primary = useThemeColor('primary');
  const primaryFg = useThemeColor('primary-foreground');
  const foregroundOnCard = useThemeColor('foreground');

  // Spring-in: pill scales 0.85 → 1 + fades in; the snapshot
  // bubble scales 1 → SNAPSHOT_SCALE so it "lifts".
  const enter = React.useRef(new Animated.Value(0)).current;
  React.useEffect(() => {
    Animated.spring(enter, {
      toValue: 1,
      useNativeDriver: true,
      damping: 16,
      stiffness: 220,
      mass: 0.7,
    }).start();
  }, [enter]);

  const handleCopy = React.useCallback(() => {
    const body = target.body;
    onClose();
    void (async () => {
      try {
        await Clipboard.setStringAsync(body);
        toast.info('Copied');
      } catch {
        toast.error("Couldn't copy");
      }
    })();
  }, [target.body, onClose]);

  const actions: Action[] = [];
  if (!target.isDeleted) {
    actions.push({
      key: 'copy',
      label: 'Copy',
      icon: <Copy size={20} color={fg} />,
      onPress: handleCopy,
    });
  }
  actions.push({
    key: 'react',
    label: 'React',
    icon: <SmilePlus size={20} color={fg} />,
    onPress: () => {
      toast.info('Reactions coming soon');
      onClose();
    },
  });
  if (!target.mine) {
    actions.push({
      key: 'report',
      label: 'Report',
      icon: <Flag size={20} color={fg} />,
      onPress: () => {
        toast.info('Thanks — we’ll take a look.');
        onClose();
      },
    });
  }
  if (target.mine && !target.isDeleted) {
    actions.push({
      key: 'delete',
      label: 'Delete',
      icon: <Trash2 size={20} color={destructive} />,
      destructive: true,
      onPress: () => {
        onDelete(target.id);
        onClose();
      },
    });
  }

  // "Sent · Today  10:23 AM" — empty if the timestamp is
  // missing/unparseable (placeholder rows). Lives as a top row
  // INSIDE the pill (with a hairline under it), so it reads as
  // one unit and can never drift off-screen on its own.
  const sentLabel = (() => {
    const t = formatTimeDividerLabel(target.createdAt);
    return t ? `Sent · ${t}` : '';
  })();
  // Card width hugs its content: the wider of the icon row and the
  // timestamp caption (estimated — see CAPTION_CHAR_PX). No fixed
  // floor — for one icon + a long stamp the caption sets the width;
  // for four icons + a short stamp the icons do.
  const iconRowWidth = actions.length * ICON_BTN + ICON_ROW_PAD * 2;
  const captionWidth = sentLabel
    ? Math.ceil(sentLabel.length * CAPTION_CHAR_PX) + CAPTION_PAD * 2
    : 0;
  const pillWidth = Math.max(iconRowWidth, captionWidth);
  const pillHeight = ICON_ROW_H + (sentLabel ? CAPTION_ROW_H : 0);

  // --- Positioning -------------------------------------------------
  // Android's measureInWindow returns y relative to the content
  // window (below the translucent status bar in edge-to-edge mode),
  // but the statusBarTranslucent Modal's origin is the very top of
  // the screen — so a measured y lands ~status-bar-height too high.
  // iOS measureInWindow is already screen-absolute. Add insets.top
  // on Android only to line the snapshot up with where the bubble
  // actually sits.
  const yOffset = Platform.OS === 'android' ? insets.top : 0;
  const rect = target.rect;
  // The snapshot is pinned at the bubble's measured left/top; it
  // sizes to its content (capped like the in-thread bubble) rather
  // than to `rect.width` — a deleted "Message deleted" bubble is
  // narrow, and forcing the snapshot to that width re-wraps the
  // text and makes the card tall. Same content + same cap = same
  // width = same layout as the original.
  let snapshot: { left: number; top: number } | null = null;
  let pillStyle: { left: number; top: number };

  const topGuard = insets.top + EDGE_PAD;
  const bottomGuard = screenH - insets.bottom - EDGE_PAD;
  const snapshotMaxWidth = Math.round(screenW * SNAPSHOT_MAX_WIDTH_RATIO);

  if (rect) {
    const rectY = rect.y + yOffset;
    snapshot = { left: rect.x, top: rectY };

    // Anchor the pill to the bubble's side: mine → right edges line
    // up; theirs → left edges. Then clamp horizontally on screen.
    const bubbleRight = rect.x + rect.width;
    let pillLeft = target.mine ? bubbleRight - pillWidth : rect.x;
    pillLeft = Math.max(EDGE_PAD, Math.min(pillLeft, screenW - pillWidth - EDGE_PAD));

    // Rest above the bubble; flip below if there's no room above;
    // either way clamp the pill fully on-screen vertically.
    const aboveTop = rectY - GAP - pillHeight;
    const belowTop = rectY + rect.height + GAP;
    let pillTop = aboveTop >= topGuard ? aboveTop : belowTop;
    pillTop = Math.max(topGuard, Math.min(pillTop, bottomGuard - pillHeight));
    pillStyle = { left: pillLeft, top: pillTop };
  } else {
    // No measurement — center the pill, no snapshot.
    pillStyle = {
      left: (screenW - pillWidth) / 2,
      top: Math.max(topGuard, (screenH - pillHeight) / 2),
    };
  }

  const snapshotScale = enter.interpolate({ inputRange: [0, 1], outputRange: [1, SNAPSHOT_SCALE] });

  return (
    <Modal visible transparent animationType="fade" onRequestClose={onClose} statusBarTranslucent>
      <Pressable
        accessibilityLabel="Dismiss message actions"
        onPress={onClose}
        testID={testID}
        style={{ flex: 1 }}
        className="bg-black/55">
        {/* Lifted bubble snapshot — pinned at its original spot,
            scaled up a hair. Non-interactive: taps fall through to
            the dismiss backdrop. */}
        {snapshot ? (
          <Animated.View
            pointerEvents="none"
            style={{
              position: 'absolute',
              left: snapshot.left,
              top: snapshot.top,
              maxWidth: snapshotMaxWidth,
              transform: [{ scale: snapshotScale }],
            }}>
            <View
              style={{
                backgroundColor: target.mine ? primary : cardBg,
                maxWidth: snapshotMaxWidth,
              }}
              className={cn(
                'rounded-2xl px-3 py-2 shadow-xl shadow-black/40',
                target.mine ? 'rounded-br-sm' : 'rounded-bl-sm'
              )}>
              <Text
                numberOfLines={6}
                style={{ color: target.mine ? primaryFg : foregroundOnCard }}
                className={cn('text-base', target.isDeleted && 'italic')}>
                {target.isDeleted ? 'Message deleted' : target.body}
              </Text>
            </View>
          </Animated.View>
        ) : null}

        {/* Action pill — rests just above (or below) the bubble, on
            the bubble's side. A unified rounded card: "Sent · …"
            timestamp on top (with a hairline under it), icon row
            below. Same elevated-surface family as the chat; the
            inner Pressable swallows taps so picking inside it
            doesn't bubble to the dismiss backdrop. */}
        <Animated.View
          style={{
            position: 'absolute',
            ...pillStyle,
            width: pillWidth,
            opacity: enter,
            transform: [
              { scale: enter.interpolate({ inputRange: [0, 1], outputRange: [0.85, 1] }) },
            ],
          }}>
          <Pressable
            onPress={() => {}}
            style={{ backgroundColor: cardBg }}
            className="overflow-hidden rounded-2xl shadow-2xl shadow-black/50">
            {sentLabel ? (
              <View
                style={{ paddingHorizontal: CAPTION_PAD }}
                className="border-b border-border pb-1 pt-1.5">
                <Text
                  numberOfLines={1}
                  variant="muted"
                  className="text-center text-[11px] font-medium">
                  {sentLabel}
                </Text>
              </View>
            ) : null}
            <View
              style={{ paddingHorizontal: ICON_ROW_PAD }}
              className="flex-row items-center justify-center">
              {actions.map((a) => (
                <Pressable
                  key={a.key}
                  onPress={a.onPress}
                  accessibilityRole="button"
                  accessibilityLabel={a.label}
                  testID={`message-action-${a.key}`}
                  style={{ width: ICON_BTN, height: ICON_BTN }}
                  className="items-center justify-center rounded-full active:bg-muted">
                  {a.icon}
                </Pressable>
              ))}
            </View>
          </Pressable>
        </Animated.View>
      </Pressable>
    </Modal>
  );
}
