// Cross-platform shell for routed "modal" screens (e.g. /search,
// /conversations/new). Native gets the screen content as-is —
// expo-router's `presentation: 'modal'` already renders these as
// half-sheets. Web doesn't honor that flag, so without help these
// routes navigate fullscreen and lose all "modal" feel. This shell
// wraps the content in a centered card with a dim backdrop so a
// /search push from the sidebar reads as a modal, not a page nav.
//
// Backdrop click + Escape both call `onClose`, which the screen
// wires to its existing back/cancel handler (router.back() with a
// chats-tab fallback).
import * as React from 'react';
import { Platform, Pressable, View } from 'react-native';

const isWeb = Platform.OS === 'web';

type Props = {
  /** Native: ignored. Web: backdrop click / Esc fires this. */
  onClose: () => void;
  /** Cap the card height so long lists scroll inside instead of
   * pushing the modal off-screen. */
  maxHeightVh?: number;
  testID?: string;
  children: React.ReactNode;
};

export function ModalScreenShell({ onClose, maxHeightVh = 80, testID, children }: Props) {
  // Wire the keyboard escape so desktop users can dismiss without
  // reaching for the cancel button. Native is a no-op (no DOM).
  React.useEffect(() => {
    if (!isWeb) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose]);

  if (!isWeb) return <>{children}</>;

  // Web: backdrop + centered card. The inner card needs to clip
  // overflow so the screen's internal FlashList scrolls in-card.
  return (
    <Pressable
      accessibilityLabel="Dismiss"
      onPress={onClose}
      className="flex-1 items-center justify-center bg-black/40 p-4">
      <Pressable
        onPress={() => {}}
        testID={testID}
        style={{ maxHeight: `${maxHeightVh}vh` as unknown as number, width: '100%' }}
        className="w-full max-w-2xl overflow-hidden rounded-2xl bg-card shadow-2xl shadow-black/40">
        <View className="flex-1">{children}</View>
      </Pressable>
    </Pressable>
  );
}
