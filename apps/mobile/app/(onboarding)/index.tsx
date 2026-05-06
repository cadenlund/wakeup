// Post-login onboarding carousel (Phase 3.0). Six slides:
//
//   1. Just your friends      — friend-graph value prop.
//   2. DMs and group chats    — chat shape.
//   3. Voice + video rooms    — calls value prop.
//   4. Profile                — AvatarPicker + bio + status emoji.
//                               Display name is prefilled from /me
//                               (set at register, edited later in
//                               settings).
//   5. Theme picker           — scheme grid + light/dark/system row.
//                               Persists to the local theme store
//                               only; the backend's color_scheme
//                               column tracks light/dark/system, not
//                               the named scheme.
//   6. You're all set         — handoff CTA. The friend-search
//                               component lives in the friends tab
//                               (Phase 4) and gets re-mounted here
//                               in a follow-up.
//
// Finish: POST /v1/users/me/onboard → cache write-through with the
// returned MeResponse → AuthGate flips and routes to (tabs).
import { useRouter } from 'expo-router';
import { ChevronLeft, MessageCircleHeart, Phone, UserPlus, Users } from 'lucide-react-native';
import * as React from 'react';
import {
  AccessibilityInfo,
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
import { useFieldErrors, useTopLevelError } from '@/lib/api/use-field-errors';
import { haptics } from '@/lib/haptics';
import { Sentry } from '@/lib/sentry';
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
  // useState's initial-value runs ONCE; if the /me query resolves
  // after the carousel mounts (or if the user lands here on a re-
  // login with cached values absent from the first render) the
  // fields stay empty and a no-edit Continue would PATCH "" over
  // saved values. Hydrate via effect when the server values arrive
  // and the user hasn't touched the field yet. (CR on PR #117.)
  const [bio, setBio] = React.useState(me?.bio ?? '');
  const [statusEmoji, setStatusEmoji] = React.useState(me?.status_emoji ?? '');
  const bioTouched = React.useRef(false);
  const emojiTouched = React.useRef(false);
  // Mirror server values to local state until the user touches the
  // field. Coalesce null/undefined to '' so a server-side clear (bio
  // explicitly null) is reflected locally — without that, the
  // dirty-check on Continue would compare a stale string against ''
  // and PATCH '' on top of '' as if it were a new edit.
  React.useEffect(() => {
    if (!bioTouched.current) setBio(me?.bio ?? '');
  }, [me?.bio]);
  React.useEffect(() => {
    if (!emojiTouched.current) setStatusEmoji(me?.status_emoji ?? '');
  }, [me?.status_emoji]);
  const patchMe = usePatchV1UsersMe();
  const profileFieldErrors = useFieldErrors(patchMe.error);
  const profileTopError = useTopLevelError(patchMe.error);

  // Reduced-motion: turn off the horizontal scroll animation when the
  // OS setting is on. (CR + §10.5 accessibility baseline.)
  const [reduceMotion, setReduceMotion] = React.useState(false);
  React.useEffect(() => {
    let mounted = true;
    AccessibilityInfo.isReduceMotionEnabled().then((v) => {
      if (mounted) setReduceMotion(v);
    });
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', (v) =>
      setReduceMotion(v)
    );
    return () => {
      mounted = false;
      sub?.remove();
    };
  }, []);

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
  // Profile slide is index 3 (0=Just your friends … 3=Profile …).
  // Tracked here so the scroll handler can fire a fire-and-forget
  // save when the user swipes off it. CR rightly flagged that the
  // explicit Continue button was the only persistence path — a
  // horizontal swipe past the profile would silently drop edits.
  //
  // On swipe-off save FAILURE we scroll the user back to the
  // profile slide so they see the inline error from `topError`
  // (already wired below the Continue button) and can retry. The
  // mutationCache toast also fires for the global notification, but
  // the user has already moved past the slide by then — the
  // scroll-back is what makes the failure recoverable instead of a
  // silent data loss. Also Sentry-capture so chronic failures
  // (server outage, malformed payload) are observable.
  const PROFILE_SLIDE = 3;
  const persistProfileEditsIfDirty = React.useCallback(() => {
    const trimmedBio = bio.trim();
    const trimmedEmoji = statusEmoji.trim();
    const dirty = trimmedBio !== (me?.bio ?? '') || trimmedEmoji !== (me?.status_emoji ?? '');
    if (!dirty || patchMe.isPending) return;
    patchMe.mutate(
      { data: { bio: trimmedBio, status_emoji: trimmedEmoji } },
      {
        onError: (err) => {
          Sentry.captureException(err, {
            tags: { surface: 'onboarding-swipe-off-save' },
          });
          haptics.warning();
          // Snap back so the user can correct + retry. Without this
          // they keep swiping forward through the carousel with no
          // signal that the profile didn't persist.
          setPage(PROFILE_SLIDE);
          scrollRef.current?.scrollTo({
            x: PROFILE_SLIDE * width,
            animated: !reduceMotion,
          });
        },
      }
    );
  }, [bio, statusEmoji, me?.bio, me?.status_emoji, patchMe, width, reduceMotion]);

  const onScroll = (e: NativeSyntheticEvent<NativeScrollEvent>) => {
    const x = e.nativeEvent.contentOffset.x;
    const next = Math.round(x / Math.max(width, 1));
    if (next === page) return;
    if (page === PROFILE_SLIDE && next !== PROFILE_SLIDE) {
      persistProfileEditsIfDirty();
    }
    setPage(next);
  };

  const goTo = (target: number) => {
    setPage(target);
    scrollRef.current?.scrollTo({ x: target * width, animated: !reduceMotion });
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
                    onChangeText={(text) => {
                      bioTouched.current = true;
                      setBio(text);
                    }}
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
                    onChange={(next) => {
                      emojiTouched.current = true;
                      setStatusEmoji(next);
                    }}
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
              {profileTopError ? (
                <Text
                  testID="onboarding-profile-top-error"
                  className="pt-2 text-center text-sm text-destructive">
                  {profileTopError}
                </Text>
              ) : null}
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
