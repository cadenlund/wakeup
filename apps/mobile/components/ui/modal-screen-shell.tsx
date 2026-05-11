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
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const isWeb = Platform.OS === 'web';

type Props = {
  /** Native: ignored. Web: backdrop click / Esc fires this. */
  onClose: () => void;
  /** Cap the card height so long lists scroll inside instead of
   * pushing the modal off-screen. */
  maxHeightVh?: number;
  /** Floor the card height (web only) so an empty list state
   * doesn't collapse the modal to a tiny box mid-screen.
   * Defaults to 60vh — same vertical bounds as the search modal
   * + create-group flow, so every modal feels the same size. */
  minHeightVh?: number;
  testID?: string;
  children: React.ReactNode;
};

export function ModalScreenShell({
  onClose,
  maxHeightVh = 80,
  minHeightVh = 60,
  testID,
  children,
}: Props) {
  // Wire the keyboard escape so desktop users can dismiss without
  // reaching for the cancel button. Native is a no-op (no DOM).
  // Listener runs in the capture phase so an autofocused
  // <TextInput> inside the modal can't swallow the Escape key
  // before us — react-native-web's TextInput calls
  // preventDefault on some Escape paths, and a bubble-phase
  // listener never sees the event when that happens.
  React.useEffect(() => {
    if (!isWeb) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', handler, { capture: true });
    return () => window.removeEventListener('keydown', handler, { capture: true });
  }, [onClose]);

  // Native: the route's `presentation: 'modal'` gives us the iOS
  // bottom-sheet with system chrome (rounded top, status bar
  // padding, drag-to-dismiss); just pass through. Android's
  // 'modal' presentation, by contrast, animates fullscreen with
  // no status-bar inset — so the search input + close button get
  // covered by the system clock / battery icons. Pad the top by
  // the insets value when running on Android. (Hook is also
  // called on iOS to keep call order stable, but the value is 0
  // there because the system chrome already inset us.)
  const insets = useSafeAreaInsets();
  if (!isWeb) {
    if (Platform.OS === 'android') {
      return <View style={{ flex: 1, paddingTop: insets.top }}>{children}</View>;
    }
    return <>{children}</>;
  }

  // Web: backdrop + centered card. Pinned to the viewport via
  // `position: fixed` so it overlays whatever route is rendered
  // underneath rather than being a stacking child of the routed
  // pane (which would push the previous page out of the viewport
  // and leave just the bare bg-background visible behind the
  // backdrop). The inner card clips overflow so internal lists
  // scroll inside the card.
  return (
    <Pressable
      accessibilityLabel="Dismiss"
      onPress={onClose}
      style={{
        position: 'fixed' as unknown as 'absolute',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        zIndex: 50,
      }}
      className="items-center justify-center bg-black/50 p-4">
      <Pressable
        // This Pressable exists ONLY to swallow taps so a click on
        // the card surface doesn't bubble up to the backdrop and
        // dismiss the modal. It has no semantic action. accessibilityRole
        // 'none' tells screen readers to skip it; we still give it
        // an accessibilityLabel so any tooling (Maestro tap-by-label,
        // testing-library) that walks the tree by label finds a
        // stable identifier per the project's a11y baseline.
        accessibilityRole="none"
        accessibilityLabel="Modal content"
        onPress={() => {}}
        testID={testID}
        style={{
          maxHeight: `${maxHeightVh}vh` as unknown as number,
          minHeight: `${minHeightVh}vh` as unknown as number,
          width: '100%',
        }}
        className="w-full max-w-2xl overflow-hidden rounded-2xl bg-card shadow-2xl shadow-black/40">
        <View className="flex-1">{children}</View>
      </Pressable>
    </Pressable>
  );
}
