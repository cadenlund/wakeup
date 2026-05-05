// Native variant of the auth screen layout. The form sits inside a
// rounded card centered on a primary-tinted background with two
// soft offset circles — mirrors the web hero look so the brand
// experience is consistent across platforms.
//
// The layout owns scroll + keyboard handling so individual screens
// just declare their form fields as direct children. (Screens that
// need their own scroll behaviour can opt out by importing the
// raw children pattern, but auth flows are simple enough that one
// pattern fits all of them.)
import * as React from 'react';
import { KeyboardAvoidingView, Platform, ScrollView, View } from 'react-native';

export function AuthScreenLayout({ children }: { children: React.ReactNode }) {
  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      className="flex-1 bg-primary">
      {/* Decorative bg accents — pointer-events none so they don't
          steal taps. Translated off-canvas so only the corner soft
          glow lands inside the visible area. */}
      <View style={{ pointerEvents: 'none' }} className="absolute inset-0 overflow-hidden">
        <View className="absolute -right-32 -top-32 h-[420px] w-[420px] rounded-full bg-accent opacity-40" />
        <View className="absolute -bottom-40 -left-24 h-[380px] w-[380px] rounded-full bg-secondary opacity-30" />
      </View>

      <ScrollView
        contentContainerClassName="flex-grow items-center justify-center px-4 py-10"
        keyboardShouldPersistTaps="handled">
        <View className="w-full max-w-md rounded-3xl bg-card p-6 shadow-2xl shadow-black/20">
          {children}
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
