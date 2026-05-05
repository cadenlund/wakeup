// Settings · Theme — canonical home for the 11-swatch picker (10
// schemes + system) per WAKEUPEXPO.md §4.5. The mode-preview tile
// at the top renders a chat bubble pair against the active scheme's
// inline colors so a tap on a swatch flashes both the chrome and a
// real product surface — the previous version embedded in the
// Profile tab moves here so this is the one source of truth.
import { Settings as SettingsIcon } from 'lucide-react-native';
import * as React from 'react';
import { Pressable, ScrollView, View } from 'react-native';

import { Card, CardContent } from '@/components/ui/card';
import { Text } from '@/components/ui/text';
import { SCHEMES, type Scheme, type SchemeOrSystem } from '@/lib/theme/schemes';
import { useThemeStore, type ModePreference } from '@/lib/theme/store';
import { useThemeColor } from '@/lib/theme/use-theme-color';

// Visual swatch colors per scheme — anchored to each scheme's primary
// from global.css §4.5. Used purely for the picker preview tile so we
// can show a swatch without resolving CSS variables in JS. Should
// stay loosely in sync with the @theme blocks; if a palette evolves,
// nudge the hex here too.
const SWATCH_COLORS: Record<
  Scheme | 'system',
  { bg: string; primary: string; muted: string; foreground: string; primaryFg: string }
> = {
  sunrise: { bg: '#fff8ee', primary: '#ff8c5a', muted: '#ffe8d4', foreground: '#4a2511', primaryFg: '#ffffff' },
  daylight: { bg: '#fafafa', primary: '#1e40af', muted: '#f1f5f9', foreground: '#0f172a', primaryFg: '#ffffff' },
  noon: { bg: '#ffffff', primary: '#fbbf24', muted: '#fffcf0', foreground: '#1a1a1a', primaryFg: '#1a1a1a' },
  golden: { bg: '#fffbea', primary: '#b45309', muted: '#fef3c7', foreground: '#3b2103', primaryFg: '#fffbea' },
  meadow: { bg: '#f0fdf4', primary: '#15803d', muted: '#dcfce7', foreground: '#052e16', primaryFg: '#f0fdf4' },
  dusk: { bg: '#1e293b', primary: '#d97706', muted: '#334155', foreground: '#f1f5f9', primaryFg: '#fffbeb' },
  twilight: { bg: '#0f172a', primary: '#4f46e5', muted: '#1e293b', foreground: '#e2e8f0', primaryFg: '#eef2ff' },
  aurora: { bg: '#082f49', primary: '#22d3ee', muted: '#0c4a6e', foreground: '#f0f9ff', primaryFg: '#082f49' },
  midnight: { bg: '#020617', primary: '#3b82f6', muted: '#0f172a', foreground: '#e2e8f0', primaryFg: '#020617' },
  rem: { bg: '#1e1b4b', primary: '#ec4899', muted: '#312e81', foreground: '#ede9fe', primaryFg: '#1e1b4b' },
  system: { bg: '#94a3b8', primary: '#475569', muted: '#cbd5e1', foreground: '#0f172a', primaryFg: '#ffffff' },
};

const MODE_OPTIONS: { value: ModePreference; label: string }[] = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'system', label: 'Auto' },
];

const PICKER_ITEMS: SchemeOrSystem[] = ['system', ...SCHEMES.map((s) => s.id)];

function PreviewTile({ schemeId }: { schemeId: SchemeOrSystem }) {
  const c = SWATCH_COLORS[schemeId];
  return (
    <View
      style={{
        borderRadius: 20,
        overflow: 'hidden',
        backgroundColor: c.bg,
        borderWidth: 1,
        borderColor: 'rgba(0,0,0,0.08)',
      }}>
      <View style={{ paddingHorizontal: 16, paddingVertical: 18, gap: 10 }}>
        <View style={{ flexDirection: 'row', justifyContent: 'flex-start' }}>
          <View
            style={{
              maxWidth: '78%',
              backgroundColor: c.muted,
              paddingHorizontal: 12,
              paddingVertical: 8,
              borderRadius: 14,
              borderBottomLeftRadius: 4,
            }}>
            <Text style={{ color: c.foreground, fontSize: 13 }}>
              that snare is heaven 😮‍💨
            </Text>
          </View>
        </View>
        <View style={{ flexDirection: 'row', justifyContent: 'flex-end' }}>
          <View
            style={{
              maxWidth: '78%',
              backgroundColor: c.primary,
              paddingHorizontal: 12,
              paddingVertical: 8,
              borderRadius: 14,
              borderBottomRightRadius: 4,
            }}>
            <Text style={{ color: c.primaryFg, fontSize: 13 }}>same — pull it back 1db?</Text>
          </View>
        </View>
      </View>
    </View>
  );
}

type SwatchProps = {
  id: SchemeOrSystem;
  label: string;
  Icon: React.ComponentType<{ size?: number; color?: string }>;
  selected: boolean;
  onPress: () => void;
};

function ThemeSwatch({ id, label, Icon, selected, onPress }: SwatchProps) {
  const c = SWATCH_COLORS[id];
  const ringColor = useThemeColor('ring');
  const isLightish = id === 'sunrise' || id === 'daylight' || id === 'noon' || id === 'golden' || id === 'meadow';
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={`Activate ${label} theme`}
      accessibilityState={{ selected }}
      style={{
        width: '31%',
        aspectRatio: 1,
        borderRadius: 16,
        backgroundColor: c.bg,
        borderWidth: selected ? 2.5 : 1,
        borderColor: selected ? ringColor : 'rgba(0,0,0,0.08)',
        padding: 12,
        justifyContent: 'space-between',
      }}>
      <View
        style={{
          width: 32,
          height: 32,
          borderRadius: 16,
          backgroundColor: c.primary,
          alignItems: 'center',
          justifyContent: 'center',
        }}>
        <Icon size={16} color={c.primaryFg} />
      </View>
      <Text
        className="text-xs font-semibold"
        style={{ color: isLightish ? '#0f172a' : '#ffffff' }}>
        {label}
      </Text>
    </Pressable>
  );
}

export default function ThemePickerScreen() {
  const selected = useThemeStore((s) => s.selected);
  const setScheme = useThemeStore((s) => s.setScheme);
  const modePreference = useThemeStore((s) => s.modePreference);
  const setModePreference = useThemeStore((s) => s.setModePreference);
  const fg = useThemeColor('foreground');
  const mutedFg = useThemeColor('muted-foreground');
  const ringColor = useThemeColor('ring');

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-6 gap-6 pb-12">
      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Preview
        </Text>
        <PreviewTile schemeId={selected} />
        <Text variant="small" style={{ color: mutedFg }}>
          A small chat bubble pair, rendered in the active scheme. Tap a swatch below to flip it.
        </Text>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Mode
        </Text>
        <Card>
          <CardContent className="flex-row gap-2 py-3">
            {MODE_OPTIONS.map((opt) => {
              const active = opt.value === modePreference;
              return (
                <Pressable
                  key={opt.value}
                  onPress={() => {
                    void setModePreference(opt.value);
                  }}
                  accessibilityRole="button"
                  accessibilityState={{ selected: active }}
                  style={{
                    flex: 1,
                    paddingVertical: 10,
                    borderRadius: 10,
                    alignItems: 'center',
                    backgroundColor: active ? ringColor : 'transparent',
                    borderWidth: 1,
                    borderColor: active ? ringColor : 'rgba(100, 116, 139, 0.25)',
                  }}>
                  <Text
                    style={{ color: active ? '#ffffff' : fg, fontWeight: '600' }}>
                    {opt.label}
                  </Text>
                </Pressable>
              );
            })}
          </CardContent>
        </Card>
        <Text variant="small" style={{ color: mutedFg }}>
          Mode (light · dark · auto) is independent from scheme — every scheme has both moods.
        </Text>
      </View>

      <View className="gap-2">
        <Text variant="small" className="font-semibold uppercase tracking-wider text-muted-foreground">
          Sleep cycle scheme
        </Text>
        <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8, rowGap: 8 }}>
          {PICKER_ITEMS.map((id) => {
            const meta = id === 'system' ? null : SCHEMES.find((s) => s.id === id);
            const label = id === 'system' ? 'System' : (meta?.label ?? id);
            const Icon = (meta?.icon ?? SettingsIcon) as React.ComponentType<{
              size?: number;
              color?: string;
            }>;
            return (
              <ThemeSwatch
                key={id}
                id={id}
                label={label}
                Icon={Icon}
                selected={selected === id}
                onPress={() => {
                  void setScheme(id);
                }}
              />
            );
          })}
        </View>
        <Text variant="small" style={{ color: mutedFg }}>
          Schemes follow the day-into-sleep arc: sunrise → daylight → noon → golden → meadow → dusk →
          twilight → aurora → midnight → rem.
        </Text>
      </View>
    </ScrollView>
  );
}
