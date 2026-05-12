// The "me" card at the top of the settings screen — avatar, display
// name, bio, status emoji. Reuses the onboarding building blocks
// (<AvatarPicker>, <StatusEmojiPicker>) and the same PATCH /v1/users/me
// mutation. Fields hydrate from the cached /me row (and re-hydrate when
// it resolves after mount) until the user touches them; "Save" is
// enabled only when something changed and writes through to the
// me-query cache so the rest of the app picks it up in the same paint.
import * as React from 'react';
import { View } from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { AvatarPicker } from '@/components/avatar-picker';
import { StatusEmojiPicker } from '@/components/onboarding/status-emoji-picker';
import { Button } from '@/components/ui/button';
import { FieldError } from '@/components/ui/field-error';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey, useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { usePatchV1UsersMe } from '@/lib/api/hooks/users/users';
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { haptics } from '@/lib/haptics';
import { toast } from '@/lib/toast';

type MeShape = {
  display_name?: string;
  bio?: string;
  status_emoji?: string;
  avatar_url?: string | null;
};

export function ProfileCard() {
  const qc = useQueryClient();
  // apiFetch returns the unwrapped JSON body; orval types it as the
  // {data,status,headers} wrapper. Cast to the runtime MeResponse.
  const { data: meEnvelope } = useGetV1AuthMe({ query: { staleTime: 60_000 } });
  const me = meEnvelope as MeShape | undefined;

  const [displayName, setDisplayName] = React.useState(me?.display_name ?? '');
  const [bio, setBio] = React.useState(me?.bio ?? '');
  const [statusEmoji, setStatusEmoji] = React.useState(me?.status_emoji ?? '');
  // Mirror server values until the field is touched, so a late /me
  // resolve (or a re-login) doesn't leave fields empty and let a
  // no-op Save PATCH "" over saved values. (CR on PR #117.)
  const nameTouched = React.useRef(false);
  const bioTouched = React.useRef(false);
  const emojiTouched = React.useRef(false);
  React.useEffect(() => {
    if (!nameTouched.current) setDisplayName(me?.display_name ?? '');
  }, [me?.display_name]);
  React.useEffect(() => {
    if (!bioTouched.current) setBio(me?.bio ?? '');
  }, [me?.bio]);
  React.useEffect(() => {
    if (!emojiTouched.current) setStatusEmoji(me?.status_emoji ?? '');
  }, [me?.status_emoji]);

  const patchMe = usePatchV1UsersMe({
    mutation: {
      onSuccess: async () => {
        haptics.success();
        toast.success('Profile updated');
        nameTouched.current = false;
        bioTouched.current = false;
        emojiTouched.current = false;
        await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
      },
    },
  });
  const fieldErrors = useFieldErrors(patchMe.error);
  const topError = useTopLevelError(patchMe.error);

  const trimmedName = displayName.trim();
  const trimmedBio = bio.trim();
  const trimmedEmoji = statusEmoji.trim();
  const dirty =
    trimmedName !== (me?.display_name ?? '') ||
    trimmedBio !== (me?.bio ?? '') ||
    trimmedEmoji !== (me?.status_emoji ?? '');
  // display_name has a min length of 1 server-side — don't let Save
  // submit an empty one.
  const canSave = dirty && trimmedName.length > 0 && !patchMe.isPending;

  const save = () => {
    if (!canSave) return;
    patchMe.mutate({
      data: { display_name: trimmedName, bio: trimmedBio, status_emoji: trimmedEmoji },
    });
  };

  return (
    <View className="gap-5 rounded-2xl border border-border bg-card p-5">
      <AvatarPicker
        avatarUrl={me?.avatar_url}
        displayName={me?.display_name}
        surface="card"
        testID="settings-avatar"
      />

      <View className="gap-2">
        <Label nativeID="settings-name-label">Display name</Label>
        <Input
          testID="settings-display-name"
          accessibilityLabel="Display name"
          aria-labelledby="settings-name-label"
          value={displayName}
          onChangeText={(t) => {
            nameTouched.current = true;
            setDisplayName(t);
          }}
          maxLength={64}
          editable={!patchMe.isPending}
        />
        <FieldError message={fieldErrors.display_name} />
      </View>

      <View className="gap-2">
        <Label nativeID="settings-bio-label">Bio</Label>
        <Input
          testID="settings-bio"
          accessibilityLabel="Bio"
          aria-labelledby="settings-bio-label"
          value={bio}
          onChangeText={(t) => {
            bioTouched.current = true;
            setBio(t);
          }}
          multiline
          maxLength={280}
          style={{ height: 88, paddingTop: 10, textAlignVertical: 'top' }}
          placeholder="Tell your friends what you're up to."
          editable={!patchMe.isPending}
        />
        <FieldError message={fieldErrors.bio} />
      </View>

      <View className="gap-2">
        <Label nativeID="settings-emoji-label">Status emoji</Label>
        <StatusEmojiPicker
          testID="settings-emoji"
          value={statusEmoji}
          onChange={(next) => {
            emojiTouched.current = true;
            setStatusEmoji(next);
          }}
          disabled={patchMe.isPending}
        />
        <FieldError message={fieldErrors.status_emoji} />
      </View>

      <Button
        testID="settings-profile-save"
        accessibilityRole="button"
        accessibilityLabel="Save profile"
        disabled={!canSave}
        onPress={save}>
        <Text>{patchMe.isPending ? 'Saving…' : 'Save'}</Text>
      </Button>
      {topError ? (
        <Text testID="settings-profile-error" className="text-center text-sm text-destructive">
          {topError}
        </Text>
      ) : null}
    </View>
  );
}
