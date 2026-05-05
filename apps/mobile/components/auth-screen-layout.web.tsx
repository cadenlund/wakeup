// Web variant of the auth screen layout.
//   - >=1024px: two-pane — hero on the left (gradient + brand
//     lockup + tagline), form column on the right inside the
//     card.
//   - <1024px: single column with the same primary-tinted bg as
//     native, form floats inside a card. Matches the mobile feel
//     for narrow browsers and DevTools-mobile preview.
import { Moon, Sparkles } from 'lucide-react-native';
import * as React from 'react';
import { KeyboardAvoidingView, Platform, ScrollView, View } from 'react-native';

import { Text } from '@/components/ui/text';

export function AuthScreenLayout({ children }: { children: React.ReactNode }) {
  return (
    <View className="flex-1 flex-row bg-primary">
      {/* Hero panel — visible only on lg+ viewports */}
      <View className="hidden flex-1 overflow-hidden bg-primary lg:flex">
        <View
          className="absolute -right-32 -top-32 h-[600px] w-[600px] rounded-full bg-accent opacity-40"
          aria-hidden
        />
        <View
          className="absolute -bottom-40 -left-32 h-[500px] w-[500px] rounded-full bg-secondary opacity-30"
          aria-hidden
        />

        <View className="flex-1 items-center justify-center p-16">
          <View className="max-w-md gap-10">
            <View className="flex-row items-center gap-3">
              <View className="rounded-2xl bg-primary-foreground/15 p-3">
                <Moon size={32} color="white" />
              </View>
              <Text className="text-3xl font-bold tracking-tight text-primary-foreground">
                Wakeup
              </Text>
            </View>

            <View className="gap-5">
              <Text className="text-5xl font-bold leading-tight tracking-tight text-primary-foreground">
                Stay close to your friends.{'\n'}Even at midnight.
              </Text>
              <Text className="text-lg leading-7 text-primary-foreground/80">
                Friend-graph chat with calls, presence, and a theme that follows your sleep cycle.
              </Text>
            </View>

            <View className="flex-row items-center gap-2 pt-2">
              <Sparkles size={16} color="white" style={{ opacity: 0.7 }} />
              <Text className="text-sm text-primary-foreground/70">
                10 sleep-cycle themes, biometric lock, end-to-end calls.
              </Text>
            </View>
          </View>
        </View>
      </View>

      {/* Form pane. On lg+: clean bg-background background, no card
          (the hero on the left already anchors the layout). On
          smaller viewports: same colourful bg + card as native. */}
      <View className="flex-1 lg:bg-background">
        {/* lg+: plain centred form */}
        <KeyboardAvoidingView
          behavior={Platform.OS === 'ios' ? 'padding' : undefined}
          className="hidden flex-1 lg:flex">
          <ScrollView
            contentContainerClassName="flex-grow items-center justify-center px-12 py-16"
            keyboardShouldPersistTaps="handled">
            <View className="w-full max-w-md">{children}</View>
          </ScrollView>
        </KeyboardAvoidingView>

        {/* <lg: card on tinted bg, mirrors native */}
        <View className="flex-1 overflow-hidden lg:hidden">
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
        </View>
      </View>
    </View>
  );
}
