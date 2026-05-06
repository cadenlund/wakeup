// Single slide layout shared by every step in the post-login
// onboarding carousel. Hands the carousel chrome (bg + accent
// circles + skip button + paginator + footer button) one place
// to live so each slide just declares its hero + body + footer
// CTA. The footer is positioned for both phone (bottom-aligned)
// and tablet/web (max-w container) without each slide repeating
// flex math.
import * as React from 'react';
import { ScrollView, View } from 'react-native';

export function SlideFrame({ children }: { children: React.ReactNode }) {
  return (
    <ScrollView
      contentContainerClassName="flex-grow items-center justify-between px-8 pb-8 pt-4"
      keyboardShouldPersistTaps="handled">
      <View className="w-full max-w-md flex-1 justify-between gap-8">{children}</View>
    </ScrollView>
  );
}
