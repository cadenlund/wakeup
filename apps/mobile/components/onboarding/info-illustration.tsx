// Hero block for an info-only onboarding slide. A small icon chip at
// the top and a large empty white card below — placeholder slot for
// the eventual product screenshot or animated GIF. Different slides
// pass different `Icon`s, but the empty card stays uniform so the
// "drop your asset here" intent reads clearly during design review.
import * as React from 'react';
import { View } from 'react-native';
import type { LucideIcon } from 'lucide-react-native';

import { useThemeColor } from '@/lib/theme/use-theme-color';

type Props = {
  Icon: LucideIcon;
};

export function InfoIllustration({ Icon }: Props) {
  const fg = useThemeColor('primary-foreground');

  return (
    <View className="items-center gap-6">
      {/* Icon chip */}
      <View className="rounded-2xl bg-primary-foreground/20 p-4">
        <Icon size={36} color={fg} strokeWidth={2} />
      </View>

      {/* Empty white placeholder — drop the screenshot or GIF here. */}
      <View className="aspect-[4/3] w-full rounded-3xl bg-primary-foreground shadow-2xl shadow-black/20" />
    </View>
  );
}
