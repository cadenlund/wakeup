// Round, tappable profile-photo control. The display half renders
// either the user's existing avatar (cached via expo-image) or a
// coloured initial chip — the upload half opens a small action
// sheet with "Take photo / Choose from library / Remove photo",
// runs the corresponding mutation, and pushes the returned
// MeResponse straight into the me-query cache so every observer
// that reads `avatar_url` (the onboarding card, the tabs header,
// future profile/settings) flips in the same paint.
//
// Designed for reuse: the onboarding profile slide passes the live
// `me` row in; settings will mount the same component once that
// screen lands. No screen-specific styling lives here — sizing is
// driven by the `size` prop and theme tokens drive every colour.
//
// Web caveat: launchCameraAsync isn't supported in browsers, so we
// hide the "Take photo" action on web. Library picking and FormData
// upload work on every platform thanks to the apiFetch FormData
// branch in lib/api/client.ts.
import { Image } from 'expo-image';
import * as ImagePicker from 'expo-image-picker';
import { Camera, ImagePlus, Trash2 } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Modal, Platform, Pressable, View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey } from '@/lib/api/hooks/auth/auth';
import { useDeleteV1UsersMeAvatar, usePostV1UsersMeAvatar } from '@/lib/api/hooks/users/users';
import { haptics } from '@/lib/haptics';
import { useThemeColor } from '@/lib/theme/use-theme-color';
import { toast } from '@/lib/toast';

type Props = {
  avatarUrl?: string | null;
  displayName?: string | null;
  size?: number;
  // Which surface the picker sits on, so the fallback chip + caption
  // read correctly. `'screen'` (default) is the onboarding case — the
  // picker floats on the `bg-primary` slide, so the chip is a
  // translucent-white circle with white text + a white-ish caption.
  // `'card'` is the settings case — on a `bg-card` surface white-on-
  // white is invisible, so the chip becomes a muted bordered circle
  // with foreground-colour text and a muted caption.
  surface?: 'screen' | 'card';
  testID?: string;
};

const DEFAULT_SIZE = 96;

export function AvatarPicker({
  avatarUrl,
  displayName,
  size = DEFAULT_SIZE,
  surface = 'screen',
  testID,
}: Props) {
  const qc = useQueryClient();
  const fg = useThemeColor('primary-foreground');
  const cardFg = useThemeColor('card-foreground');
  const onCard = surface === 'card';
  // Fallback-initial colour: white on the primary slide, foreground on
  // a card. (The spinner overlay sits on a dark scrim, so white reads
  // fine on either surface.)
  const initialColor = onCard ? cardFg : fg;

  const [sheetOpen, setSheetOpen] = React.useState(false);
  const closeSheet = React.useCallback(() => setSheetOpen(false), []);

  // Both the upload and the remove paths land on the same shape
  // ({user: MeResponse}); funnel them through one cache-update
  // helper so the picker can flip the avatar without a refetch.
  const applyMeUpdate = React.useCallback(
    async (response: unknown) => {
      haptics.success();
      const body = response as { user?: { id?: string } } | undefined;
      if (body?.user?.id) {
        qc.setQueryData(getGetV1AuthMeQueryKey(), body.user);
      }
      await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
    },
    [qc]
  );

  const upload = usePostV1UsersMeAvatar({
    mutation: {
      onSuccess: applyMeUpdate,
      onError: () => {
        toast.error('Upload failed', "We couldn't save that photo. Try again?");
      },
    },
  });

  const removeAvatar = useDeleteV1UsersMeAvatar({
    mutation: {
      onSuccess: applyMeUpdate,
      onError: () => {
        toast.error('Could not remove photo', 'Try again in a moment.');
      },
    },
  });

  const busy = upload.isPending || removeAvatar.isPending;

  const onTapAvatar = () => {
    if (busy) return;
    haptics.tap();
    setSheetOpen(true);
  };

  const handleAsset = async (asset: ImagePicker.ImagePickerAsset) => {
    const file = await assetToFormDataFile(asset);
    if (!file) {
      // assetToFormDataFile returns null when the web fetch+blob
      // path fails — surface a toast so the user isn't left wondering
      // why nothing happened. Native paths can't fail here.
      toast.error("Couldn't read photo", 'Pick another image and try again.');
      return;
    }
    // mutate (not mutateAsync) so a backend failure flows through
    // the mutation's onError → toast. Using mutateAsync would also
    // bubble the rejection out of this async fn as an unhandled
    // promise — toast already covers UX, the rejection would just
    // hit Sentry as noise. (CR on PR #117.)
    upload.mutate({
      // Cast — orval's body type expects Blob, but on RN the
      // {uri,name,type} shape is what FormData accepts. Wire payload
      // is identical; the type assertion is build-time only.
      data: { file: file as unknown as Blob },
    });
  };

  const onPickLibrary = async () => {
    closeSheet();
    const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
    if (!perm.granted) {
      toast.error('Photos access denied', 'Enable photo access in Settings to pick a picture.');
      return;
    }
    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ['images'],
      allowsEditing: true,
      aspect: [1, 1],
      quality: 0.85,
    });
    if (result.canceled) return;
    const asset = result.assets?.[0];
    if (asset) await handleAsset(asset);
  };

  const onPickCamera = async () => {
    closeSheet();
    const perm = await ImagePicker.requestCameraPermissionsAsync();
    if (!perm.granted) {
      toast.error('Camera access denied', 'Enable camera access in Settings to take a picture.');
      return;
    }
    const result = await ImagePicker.launchCameraAsync({
      allowsEditing: true,
      aspect: [1, 1],
      quality: 0.85,
    });
    if (result.canceled) return;
    const asset = result.assets?.[0];
    if (asset) await handleAsset(asset);
  };

  const onRemove = () => {
    closeSheet();
    // mutate (not mutateAsync) — same reasoning as handleAsset.
    removeAvatar.mutate();
  };

  const initial = (displayName?.trim()?.[0] ?? '?').toUpperCase();
  const hasAvatar = !!avatarUrl;

  return (
    <View className="items-center gap-2">
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={hasAvatar ? 'Change profile picture' : 'Add profile picture'}
        accessibilityState={{ busy, disabled: busy }}
        testID={testID}
        disabled={busy}
        onPress={onTapAvatar}
        style={{ width: size, height: size }}>
        <View
          className={
            onCard
              ? 'overflow-hidden rounded-full border border-border bg-muted'
              : 'overflow-hidden rounded-full bg-primary-foreground/20'
          }
          style={{ width: size, height: size }}>
          {hasAvatar ? (
            <Image
              source={{ uri: avatarUrl as string }}
              style={{ width: size, height: size }}
              contentFit="cover"
              cachePolicy="memory-disk"
              transition={150}
            />
          ) : (
            <View className="flex-1 items-center justify-center">
              <Text
                // RN's auto line-height + Android font padding crops a
                // single big capital letter (descender-heavy / tall-cap
                // fonts read as "chopped"). Pin the line box explicitly
                // and turn off Android's extra padding so the glyph
                // sits flush-centered in the circle.
                style={{
                  color: initialColor,
                  fontSize: size * 0.42,
                  lineHeight: size * 0.5,
                  includeFontPadding: false,
                  textAlign: 'center',
                  textAlignVertical: 'center',
                }}
                className="font-semibold">
                {initial}
              </Text>
            </View>
          )}
          {busy ? (
            <View className="absolute inset-0 items-center justify-center bg-black/40">
              <ActivityIndicator color={fg} />
            </View>
          ) : null}
        </View>

        {/* Edit chip pinned to the bottom-right of the circle. The
            border-card colour matches whatever surface the picker
            sits on, so the chip "punches out" of the avatar. */}
        <View className="absolute bottom-0 right-0 rounded-full border-2 border-card bg-primary p-1.5">
          <ImagePlus size={14} color={fg} />
        </View>
      </Pressable>

      <Text
        className={onCard ? 'text-xs text-muted-foreground' : 'text-xs text-primary-foreground/70'}>
        {hasAvatar ? 'Tap to change' : 'Tap to add a photo'}
      </Text>

      {/* Action sheet — bottom drawer on native, centered card on
          web. The `sm:` breakpoint approach Tailwind would normally
          handle here doesn't fire reliably on RN-web in every
          configuration, so we branch explicitly on Platform.OS.
          Plain View instead of a Pressable around the card so each
          row owns its own tap handling. */}
      <Modal visible={sheetOpen} transparent animationType="fade" onRequestClose={closeSheet}>
        <View
          className={
            Platform.OS === 'web'
              ? 'flex-1 items-center justify-center bg-black/40 p-4'
              : 'flex-1 justify-end bg-black/40'
          }>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Dismiss"
            onPress={closeSheet}
            className="absolute inset-0"
          />
          <View
            className={
              Platform.OS === 'web'
                ? 'w-full max-w-md rounded-2xl bg-card p-4 shadow-2xl shadow-black/40'
                : 'w-full rounded-t-3xl bg-card p-4 pb-8'
            }>
            <Text className="px-2 pb-3 pt-1 text-sm font-semibold text-card-foreground">
              Profile picture
            </Text>

            {Platform.OS !== 'web' ? (
              <SheetRow Icon={Camera} label="Take a photo" onPress={onPickCamera} color={cardFg} />
            ) : null}
            <SheetRow
              Icon={ImagePlus}
              label="Choose from library"
              onPress={onPickLibrary}
              color={cardFg}
            />
            {hasAvatar ? (
              <SheetRow
                Icon={Trash2}
                label="Remove photo"
                destructive
                onPress={onRemove}
                color={cardFg}
              />
            ) : null}
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Cancel"
              onPress={closeSheet}
              className="mt-2 items-center rounded-2xl bg-muted py-3">
              <Text className="text-sm font-semibold text-card-foreground">Cancel</Text>
            </Pressable>
          </View>
        </View>
      </Modal>
    </View>
  );
}

function SheetRow({
  Icon,
  label,
  onPress,
  destructive,
  color,
}: {
  Icon: typeof Camera;
  label: string;
  onPress: () => void;
  destructive?: boolean;
  color: string;
}) {
  // Destructive rows lean on the theme's `destructive` token (red in
  // every scheme) so the "remove photo" action visually separates
  // from the create/upload rows above it. Read it from the active
  // palette rather than a hardcoded hex so a dark scheme's destructive
  // (a lighter red, per palettes.ts) lines up with the `text-destructive`
  // class on the label.
  const destructiveColor = useThemeColor('destructive');
  const tint = destructive ? destructiveColor : color;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      onPress={onPress}
      className="flex-row items-center gap-3 rounded-2xl px-3 py-3 active:bg-muted">
      <Icon size={20} color={tint} />
      <Text
        className={`text-base ${
          destructive ? 'font-medium text-destructive' : 'text-card-foreground'
        }`}>
        {label}
      </Text>
    </Pressable>
  );
}

// Builds the per-platform multipart `file` payload. RN's FormData
// accepts the bare `{uri, name, type}` triple — passing a Blob
// crashes on Hermes for big assets. On web we fetch the picker URI
// (which is typically a `blob:`/`data:` URL) and assemble a File so
// the browser fetch sets the proper boundary + filename. iOS
// content:// URIs and Android file:// URIs both Just Work via the
// triple form on native.
async function assetToFormDataFile(
  asset: ImagePicker.ImagePickerAsset
): Promise<File | { uri: string; name: string; type: string } | null> {
  const name = filenameFromAsset(asset);
  const type = mimeFromAsset(asset);
  if (Platform.OS === 'web') {
    try {
      const res = await fetch(asset.uri);
      const blob = await res.blob();
      return new File([blob], name, { type: blob.type || type });
    } catch {
      return null;
    }
  }
  return { uri: asset.uri, name, type };
}

function filenameFromAsset(asset: ImagePicker.ImagePickerAsset): string {
  if (asset.fileName) return asset.fileName;
  // Fall back to a synthesised name based on the URI extension so
  // the backend's MIME-by-filename heuristic still has a hint when
  // the picker doesn't surface one (Android often doesn't).
  const ext = extFromUri(asset.uri) ?? extFromMime(asset.mimeType) ?? 'jpg';
  return `avatar.${ext}`;
}

function mimeFromAsset(asset: ImagePicker.ImagePickerAsset): string {
  if (asset.mimeType) return asset.mimeType;
  const ext = extFromUri(asset.uri);
  if (ext === 'png') return 'image/png';
  if (ext === 'gif') return 'image/gif';
  if (ext === 'webp') return 'image/webp';
  return 'image/jpeg';
}

function extFromUri(uri: string): string | null {
  const match = /\.([a-zA-Z0-9]+)(?:\?|$)/.exec(uri);
  return match ? match[1].toLowerCase() : null;
}

function extFromMime(mime?: string): string | null {
  if (!mime) return null;
  if (mime === 'image/jpeg') return 'jpg';
  if (mime === 'image/png') return 'png';
  if (mime === 'image/gif') return 'gif';
  if (mime === 'image/webp') return 'webp';
  return null;
}
