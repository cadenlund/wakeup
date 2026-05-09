// Cross-platform sheet primitive. Renders as a bottom drawer on
// native (touch-first surface; thumb-reachable; matches the
// friends-tab unfriend/block sheet) and as a centered modal card
// on web (mouse-first; bottom-pinned content with no thumb to
// reach for feels off in a desktop browser).
//
// Pattern:
//   <DrawerSheet visible onClose={...} testID="...">
//     ...sheet content (header + rows)
//   </DrawerSheet>
//
// The component owns the backdrop, the dismissable outer Pressable,
// and the touch-absorber inner Pressable so children just describe
// rows/labels.
import * as React from 'react';
import { Modal, Platform, Pressable, View } from 'react-native';

const isWeb = Platform.OS === 'web';

type Props = {
  visible: boolean;
  onClose: () => void;
  /** A11y label for the backdrop dismiss button. */
  dismissLabel?: string;
  testID?: string;
  children: React.ReactNode;
};

export function DrawerSheet({
  visible,
  onClose,
  dismissLabel = 'Dismiss',
  testID,
  children,
}: Props) {
  // Esc closes — RN Modal's onRequestClose only fires for the
  // Android back button, not desktop keyboards, so we wire the
  // listener ourselves on web. Native is a no-op (no DOM).
  React.useEffect(() => {
    if (!isWeb || !visible) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [visible, onClose]);

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
      statusBarTranslucent>
      <Pressable
        accessibilityLabel={dismissLabel}
        onPress={onClose}
        // Native: pin to the bottom safe area so the sheet anchors
        // to the touch zone. Web: center the card.
        className={
          isWeb
            ? 'flex-1 items-center justify-center bg-black/40 p-4'
            : 'flex-1 justify-end bg-black/40'
        }>
        {/* Inner Pressable absorbs taps so they don't bubble to the
            backdrop and dismiss the sheet. The View wrapper exists
            so the inner press-stopper doesn't grow to fill the
            backdrop on web's flex centering. */}
        <View className={isWeb ? 'w-full max-w-md' : 'w-full'}>
          <Pressable
            onPress={() => {}}
            testID={testID}
            className={
              isWeb
                ? 'overflow-hidden rounded-2xl bg-card shadow-2xl shadow-black/40'
                : 'rounded-t-3xl bg-card'
            }>
            {/* Drag-handle pill, native only — desktop modals don't
                need to communicate "swipe me down". */}
            {!isWeb ? (
              <View className="items-center pt-3">
                <View className="h-1 w-12 rounded-full bg-muted-foreground/30" />
              </View>
            ) : null}
            {children}
          </Pressable>
        </View>
      </Pressable>
    </Modal>
  );
}
