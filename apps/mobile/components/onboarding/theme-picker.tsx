// Visual theme picker shared by the onboarding carousel and the
// profile/settings screen (Phase 6+). Each scheme renders as a
// chunky swatch card showing the four anchor palette colours
// (background → primary → secondary → accent) plus the scheme
// label, so the user actually sees what they're picking instead of
// reading raw scheme names.
//
// `system` is rendered as a special card with a half-and-half look
// (light-mode primary on top, dark-mode primary on bottom) to
// communicate "follows the OS" without baking that into a
// scheme-style entry in the palette table.
import * as React from 'react';
import { Pressable, View } from 'react-native';

import { Text } from '@/components/ui/text';
import { PALETTES } from '@/lib/theme/palettes';
import { SCHEMES, type Mode, type Scheme, type SchemeOrSystem } from '@/lib/theme/schemes';

const SWATCH_TOKENS = ['background', 'primary', 'secondary', 'accent'] as const;

type Props = {
  selected: SchemeOrSystem;
  mode: Mode;
  onPick: (scheme: SchemeOrSystem) => void;
};

export function ThemePicker({ selected, mode, onPick }: Props) {
  return (
    <View className="flex-row flex-wrap" style={{ marginHorizontal: -4 }}>
      <SwatchCard
        scheme="system"
        selected={selected === 'system'}
        mode={mode}
        onPress={() => onPick('system')}
      />
      {SCHEMES.map((s) => (
        <SwatchCard
          key={s.id}
          scheme={s.id}
          selected={selected === s.id}
          mode={mode}
          onPress={() => onPick(s.id)}
        />
      ))}
    </View>
  );
}

function SwatchCard({
  scheme,
  selected,
  mode,
  onPress,
}: {
  scheme: SchemeOrSystem;
  selected: boolean;
  mode: Mode;
  onPress: () => void;
}) {
  const label = scheme.charAt(0).toUpperCase() + scheme.slice(1);
  return (
    <View style={{ width: '25%', padding: 4 }}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`Theme ${scheme}`}
        accessibilityState={{ selected }}
        onPress={onPress}
        className={`overflow-hidden rounded-xl border-2 ${
          selected ? 'border-primary' : 'border-border'
        }`}>
        <SwatchStack scheme={scheme} mode={mode} />
        <View className="bg-card px-1 py-1.5">
          <Text
            numberOfLines={1}
            className="text-center text-xs font-semibold text-card-foreground">
            {label}
          </Text>
        </View>
      </Pressable>
    </View>
  );
}

// The visual block at the top of each swatch card. For named schemes
// we paint horizontal bands of the four anchor palette colours; for
// `system` we splits vertically into the daylight (light) and
// midnight (dark) primary colours. aspect-square keeps each tile
// proportional to its column width so the grid scales with the
// container instead of being capped at a fixed height.
function SwatchStack({ scheme, mode }: { scheme: SchemeOrSystem; mode: Mode }) {
  if (scheme === 'system') {
    return (
      <View className="aspect-square flex-row">
        <View
          className="flex-1"
          style={{ backgroundColor: hsl(PALETTES.daylight.light.primary) }}
        />
        <View className="flex-1" style={{ backgroundColor: hsl(PALETTES.midnight.dark.primary) }} />
      </View>
    );
  }
  const palette = PALETTES[scheme as Scheme][mode];
  return (
    <View className="aspect-square">
      {SWATCH_TOKENS.map((token) => (
        <View key={token} className="flex-1" style={{ backgroundColor: hsl(palette[token]) }} />
      ))}
    </View>
  );
}

function hsl(triple: string): string {
  return `hsl(${triple})`;
}
