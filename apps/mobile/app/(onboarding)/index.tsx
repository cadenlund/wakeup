// Post-login onboarding carousel (Phase 3.0). Six slides:
//
//   1. Welcome — what Wakeup is.
//   2. Chat + calls — friend-graph value prop.
//   3. Sleep-cycle themes — quick teaser before the picker on
//      slide 5.
//   4. Profile — display name (prefilled from /me) + bio + status
//      emoji. Avatar upload is deferred until expo-image-picker
//      lands on a fresh dev-client build; for now it shows a
//      placeholder + "Skip for now" copy.
//   5. Theme picker — interactive scheme grid + light/dark mode
//      toggle. Persists to local theme store + writes to backend
//      via PATCH /v1/users/me/notifications.
//   6. Find your friends — username + "Add" → POST /v1/friends/
//      requests. "Skip for now" finishes onboarding without sending
//      a request.
//
// Finish: POST /v1/users/me/onboarding/complete → invalidate
// `me` query → AuthGate flips and routes to (tabs).
import { useRouter } from 'expo-router';
import { ChevronLeft, MessageCircleHeart, Phone, UserPlus, Users } from 'lucide-react-native';
import * as React from 'react';
import {
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  useWindowDimensions,
  View,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
} from 'react-native';
import { useQueryClient } from '@tanstack/react-query';

import { AvatarPicker } from '@/components/avatar-picker';
import { InfoIllustration } from '@/components/onboarding/info-illustration';
import { SlideFrame } from '@/components/onboarding/slide-frame';
import { StatusEmojiPicker } from '@/components/onboarding/status-emoji-picker';
import { ThemePicker } from '@/components/onboarding/theme-picker';
import { Button } from '@/components/ui/button';
import { FieldError } from '@/components/ui/field-error';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Text } from '@/components/ui/text';
import { getGetV1AuthMeQueryKey, useGetV1AuthMe } from '@/lib/api/hooks/auth/auth';
import { usePatchV1UsersMe, usePostV1UsersMeOnboard } from '@/lib/api/hooks/users/users';
import { useFieldErrors } from '@/lib/api/use-field-errors';
import { haptics } from '@/lib/haptics';
import { useThemeStore, type ModePreference } from '@/lib/theme/store';
import { useThemeColor } from '@/lib/theme/use-theme-color';

// ---------------------------------------------------------------------------
// Slide content — kept declarative so the order is one place to change.

const SLIDE_COUNT = 6;

const INFO_SLIDES = [
  {
    Icon: Users,
    title: 'Just your friends',
    body: 'No servers, no guilds, no random invites. Add the people you actually talk to and you’re in.',
  },
  {
    Icon: MessageCircleHeart,
    title: 'DMs and group chats',
    body: 'One-on-one or up to 25. Pinned conversations, presence dots, optimistic sends — chat that keeps up.',
  },
  {
    Icon: Phone,
    title: 'Voice + video rooms',
    body: 'Every conversation has a room. Tap the icon to drop in — no calling, no scheduling, just there.',
  },
] as const;

const MODE_PREFS: ModePreference[] = ['light', 'dark', 'system'];

// ---------------------------------------------------------------------------

export default function OnboardingScreen() {
  const router = useRouter();
  const qc = useQueryClient();
  const fg = useThemeColor('primary-foreground');
  const cardFg = useThemeColor('card-foreground');
  const scrollRef = React.useRef<ScrollView>(null);
  const { width } = useWindowDimensions();
  const [page, setPage] = React.useState(0);

  const { data: meEnvelope } = useGetV1AuthMe({
    query: { staleTime: Infinity },
  });
  // apiFetch returns the unwrapped JSON body. Orval types it as
  // `{data, status, headers}` but the runtime payload is the
  // MeResponse fields directly. Cast accordingly.
  type MeShape = {
    display_name?: string;
    bio?: string;
    status_emoji?: string;
    avatar_url?: string | null;
  };
  const me = meEnvelope as MeShape | undefined;

  // --- profile slide state ------------------------------------------------
  const [bio, setBio] = React.useState(me?.bio ?? '');
  const [statusEmoji, setStatusEmoji] = React.useState(me?.status_emoji ?? '');
  const patchMe = usePatchV1UsersMe();
  const profileFieldErrors = useFieldErrors(patchMe.error);

  // --- theme slide state --------------------------------------------------
  const selected = useThemeStore((s) => s.selected);
  const setScheme = useThemeStore((s) => s.setScheme);
  const mode = useThemeStore((s) => s.mode);
  const modePref = useThemeStore((s) => s.modePreference);
  const setModePref = useThemeStore((s) => s.setModePreference);

  // --- finalisation -------------------------------------------------------
  // The endpoint returns the updated MeResponse with `onboarded_at`
  // populated. We push that into the query cache directly so AuthGate
  // sees `onboardingDone === true` on its very next render — without
  // waiting for an invalidate→refetch round-trip. Without this, the
  // subsequent `router.replace('/')` ran while AuthGate still had a
  // null `onboarded_at` in cache, so its effect bounced the user back
  // to /(onboarding) and the carousel reset to slide 1.
  const completeOnboarding = usePostV1UsersMeOnboard({
    mutation: {
      onSuccess: async (response) => {
        haptics.success();
        // apiFetch returns the unwrapped MeResponse body; orval types
        // it as `{data, status, headers}`. Cast to the runtime shape.
        const fresh = response as unknown as { id?: string; onboarded_at?: string } | undefined;
        if (fresh?.id) {
          qc.setQueryData(getGetV1AuthMeQueryKey(), fresh);
        }
        // Belt-and-braces: also invalidate so any other observer
        // (AuthGate uses retry: false but no staleTime) refetches the
        // canonical row before they next render.
        await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
        router.replace('/(tabs)');
      },
    },
  });

  // --- carousel mechanics -------------------------------------------------
  const onScroll = (e: NativeSyntheticEvent<NativeScrollEvent>) => {
    const x = e.nativeEvent.contentOffset.x;
    const next = Math.round(x / Math.max(width, 1));
    if (next !== page) setPage(next);
  };

  const goTo = (target: number) => {
    setPage(target);
    scrollRef.current?.scrollTo({ x: target * width, animated: true });
  };

  const advance = () => goTo(Math.min(page + 1, SLIDE_COUNT - 1));
  const goBack = () => goTo(Math.max(page - 1, 0));

  // Profile slide save: best-effort PATCH; failures don't block advance.
  const saveProfileAndAdvance = async () => {
    const trimmedBio = bio.trim();
    const trimmedEmoji = statusEmoji.trim();
    const hasChanges = trimmedBio !== (me?.bio ?? '') || trimmedEmoji !== (me?.status_emoji ?? '');
    if (hasChanges) {
      try {
        await patchMe.mutateAsync({
          data: { bio: trimmedBio, status_emoji: trimmedEmoji },
        });
        await qc.invalidateQueries({ queryKey: getGetV1AuthMeQueryKey() });
      } catch {
        // toast already fires from the mutation cache; let the user
        // re-try by tapping Save again.
        return;
      }
    }
    advance();
  };

  const finish = () => completeOnboarding.mutate();

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      className="flex-1 bg-primary">
      {/* Decorative bg accents */}
      <View style={{ pointerEvents: 'none' }} className="absolute inset-0 overflow-hidden">
        <View className="absolute -right-32 -top-32 h-[420px] w-[420px] rounded-full bg-accent opacity-40" />
        <View className="absolute -bottom-40 -left-24 h-[380px] w-[380px] rounded-full bg-secondary opacity-30" />
      </View>

      {/* Top bar — Back on the left, Skip on the right. Both
          collapse on the slides where they don't apply so the
          spacing stays consistent. */}
      <View className="flex-row items-center justify-between px-4 pt-12">
        {page > 0 ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Previous slide"
            testID="onboarding-back"
            onPress={goBack}
            className="flex-row items-center gap-1 px-3 py-2">
            <ChevronLeft size={18} color={fg} />
            <Text className="text-sm font-medium text-primary-foreground/80">Back</Text>
          </Pressable>
        ) : (
          <View className="h-9 w-20" />
        )}
        {page < SLIDE_COUNT - 1 ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Skip onboarding"
            testID="onboarding-skip"
            onPress={finish}
            disabled={completeOnboarding.isPending}
            className="px-3 py-2">
            <Text className="text-sm font-medium text-primary-foreground/80">Skip</Text>
          </Pressable>
        ) : (
          <View className="h-9 w-20" />
        )}
      </View>

      <ScrollView
        ref={scrollRef}
        horizontal
        pagingEnabled
        showsHorizontalScrollIndicator={false}
        onScroll={onScroll}
        scrollEventThrottle={16}
        keyboardShouldPersistTaps="handled"
        className="flex-1">
        {/* --- Slides 1–3: informational ----------------------------- */}
        {INFO_SLIDES.map((slide, i) => (
          <View key={i} style={{ width }} className="flex-1">
            <SlideFrame>
              <View className="flex-1 justify-center gap-10">
                <InfoIllustration Icon={slide.Icon} />
                <View className="gap-3">
                  <Text className="text-center text-4xl font-bold tracking-tight text-primary-foreground">
                    {slide.title}
                  </Text>
                  <Text className="text-center text-base leading-6 text-primary-foreground/80">
                    {slide.body}
                  </Text>
                </View>
              </View>
              <Footer>
                <Button
                  size="lg"
                  variant="secondary"
                  testID={`onboarding-info-next-${i}`}
                  accessibilityRole="button"
                  accessibilityLabel="Next slide"
                  onPress={advance}>
                  <Text>Next</Text>
                </Button>
              </Footer>
            </SlideFrame>
          </View>
        ))}

        {/* --- Slide 4: profile -------------------------------------- */}
        <View style={{ width }} className="flex-1">
          <SlideFrame>
            <View className="flex-1 gap-7">
              <View className="gap-2 pt-4">
                <Text className="text-3xl font-bold tracking-tight text-primary-foreground">
                  Set up your profile
                </Text>
                <Text className="text-base text-primary-foreground/80">
                  How your friends will see you. You can change any of this later.
                </Text>
              </View>

              <AvatarPicker
                avatarUrl={me?.avatar_url}
                displayName={me?.display_name}
                testID="onboarding-avatar"
              />

              {/* Form lives inside a card so the inputs read against the
                  card surface (matches the auth screens). */}
              <View className="gap-4 rounded-3xl bg-card p-5 shadow-2xl shadow-black/20">
                <View className="gap-2">
                  <Label nativeID="onb-display-name-label">Display name</Label>
                  <Input
                    accessibilityLabel="Display name"
                    aria-labelledby="onb-display-name-label"
                    value={me?.display_name ?? ''}
                    editable={false}
                  />
                  <Text variant="muted" className="text-xs">
                    Set when you registered. Change it later in settings.
                  </Text>
                </View>

                <View className="gap-2">
                  <Label nativeID="onb-bio-label">Bio</Label>
                  <Input
                    testID="onboarding-bio"
                    accessibilityLabel="Bio"
                    aria-labelledby="onb-bio-label"
                    value={bio}
                    onChangeText={setBio}
                    multiline
                    maxLength={280}
                    // Fixed height (not minHeight) so the parent flex
                    // gap can measure the input correctly — minHeight
                    // confused iOS layout into rendering the next
                    // field's label inside the textarea.
                    style={{ height: 88, paddingTop: 10, textAlignVertical: 'top' }}
                    placeholder="Tell your friends what you're up to."
                    editable={!patchMe.isPending}
                  />
                  <FieldError message={profileFieldErrors.bio} />
                </View>

                <View className="gap-2">
                  <Label nativeID="onb-emoji-label">Status emoji</Label>
                  <StatusEmojiPicker
                    testID="onboarding-emoji"
                    value={statusEmoji}
                    onChange={setStatusEmoji}
                    disabled={patchMe.isPending}
                  />
                  <FieldError message={profileFieldErrors.status_emoji} />
                </View>
              </View>
            </View>
            <Footer>
              <Button
                size="lg"
                variant="secondary"
                testID="onboarding-profile-next"
                accessibilityRole="button"
                accessibilityLabel="Save profile and continue"
                disabled={patchMe.isPending}
                onPress={saveProfileAndAdvance}>
                <Text>{patchMe.isPending ? 'Saving…' : 'Continue'}</Text>
              </Button>
            </Footer>
          </SlideFrame>
        </View>

        {/* --- Slide 5: theme picker --------------------------------- */}
        <View style={{ width }} className="flex-1">
          <SlideFrame>
            <View className="flex-1 gap-6">
              <View className="gap-2 pt-4">
                <Text className="text-3xl font-bold tracking-tight text-primary-foreground">
                  Pick your vibe
                </Text>
                <Text className="text-base text-primary-foreground/80">
                  10 sleep-cycle themes plus your phone&apos;s system mode. Tap to try them.
                </Text>
              </View>

              <View className="rounded-3xl bg-card p-5 shadow-2xl shadow-black/20">
                <ThemePicker
                  selected={selected}
                  mode={mode}
                  onPick={(s) => {
                    haptics.tap();
                    void setScheme(s);
                  }}
                />

                <View className="mt-4 gap-2 border-t border-border pt-4">
                  <Text variant="muted" className="text-xs uppercase">
                    Light / dark
                  </Text>
                  <View className="flex-row gap-2">
                    {MODE_PREFS.map((p) => (
                      <Pressable
                        key={p}
                        accessibilityRole="button"
                        accessibilityLabel={`Mode ${p}`}
                        onPress={() => {
                          haptics.tap();
                          void setModePref(p);
                        }}
                        className={`flex-1 items-center rounded-lg px-3 py-2 ${
                          p === modePref ? 'bg-primary' : 'bg-muted'
                        }`}>
                        <Text
                          style={{ color: p === modePref ? fg : cardFg }}
                          className="text-sm font-medium capitalize">
                          {p}
                        </Text>
                      </Pressable>
                    ))}
                  </View>
                </View>
              </View>
            </View>
            <Footer>
              <Button
                size="lg"
                variant="secondary"
                testID="onboarding-theme-next"
                accessibilityRole="button"
                accessibilityLabel="Continue"
                onPress={advance}>
                <Text>Continue</Text>
              </Button>
            </Footer>
          </SlideFrame>
        </View>

        {/* --- Slide 6: friends placeholder ------------------------- */}
        {/* The actual friend-search component lives in the friends
            tab (Phase 4). We'll mount it here later once it exists
            so onboarding stays a single source of search behaviour.
            For now the slide is a clean welcome with one CTA. */}
        <View style={{ width }} className="flex-1">
          <SlideFrame>
            <View className="flex-1 items-center justify-center gap-6">
              <View className="rounded-3xl bg-primary-foreground/20 p-6">
                <UserPlus size={56} color={fg} strokeWidth={2} />
              </View>
              <View className="gap-3">
                <Text className="text-center text-3xl font-bold tracking-tight text-primary-foreground">
                  You&apos;re all set
                </Text>
                <Text className="text-center text-base leading-6 text-primary-foreground/80">
                  Find friends by username from the friends tab any time.
                </Text>
              </View>
            </View>
            <Footer>
              <Button
                size="lg"
                variant="secondary"
                testID="onboarding-finish"
                accessibilityRole="button"
                accessibilityLabel="Get started"
                disabled={completeOnboarding.isPending}
                onPress={finish}>
                <Text>{completeOnboarding.isPending ? 'Finishing…' : 'Get started'}</Text>
              </Button>
            </Footer>
          </SlideFrame>
        </View>
      </ScrollView>

      {/* Pagination dots */}
      <View className="flex-row items-center justify-center gap-2 pb-2">
        {Array.from({ length: SLIDE_COUNT }).map((_, i) => (
          <View
            key={i}
            className={`h-2 rounded-full ${
              i === page ? 'w-6 bg-primary-foreground' : 'w-2 bg-primary-foreground/40'
            }`}
          />
        ))}
      </View>
    </KeyboardAvoidingView>
  );
}

// Footer wrapper — keeps slide CTAs at a consistent vertical anchor.
function Footer({ children }: { children: React.ReactNode }) {
  return <View className="gap-2 pt-2">{children}</View>;
}
