// Chats tab's internal stack: the conversations list (`index`) and
// the thread (`conversations/[id]`). Nesting them in a <Stack> is
// what makes opening a chat *push* — so it slides in like the
// login↔register swap (those screens are headerless too) — instead
// of the flat tab-swap it was before.
//
// Headerless: the list and the thread each render their own
// in-content header bar (plain Pressable rows, not the native
// nav-bar header) so the chrome looks + presses the same on every
// platform, native or web. The Chats *tab* (in (tabs)/_layout.tsx)
// is also headerShown:false so there's no double header.
//
// `initialRouteName: 'index'` so a deep link straight to
// /conversations/<id> still mounts the list underneath it (Back
// works on a cold open). This is back-stack scaffolding only — safe
// in a nested layout, unlike at the root (see app/_layout.tsx).
import { Stack } from 'expo-router';
import * as React from 'react';

import { useThemeColor } from '@/lib/theme/use-theme-color';

export const unstable_settings = { initialRouteName: 'index' };

export default function ChatStackLayout() {
  const bg = useThemeColor('background');
  return <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: bg } }} />;
}
