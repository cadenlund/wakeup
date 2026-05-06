// Scheme registry — the ten sleep-cycle schemes from WAKEUPEXPO.md §4.5
// plus the `system` pseudo-scheme that picks a sensible default scheme
// based on the OS Appearance signal.
//
// Each scheme defines a full palette for BOTH light and dark mode in
// global.css; this module is the JS-side metadata. `mode` (light vs
// dark) is independent of `scheme` (the color personality) and lives
// on its own axis in the store — see lib/theme/store.ts.

import type { ComponentType } from 'react';
import {
  BrainCog,
  CloudSun,
  Flower,
  Moon,
  MoonStar,
  Sparkles,
  Sun,
  SunDim,
  Sunrise,
  Sunset,
  type LucideIcon,
} from 'lucide-react-native';

export type Mode = 'light' | 'dark';

export type Scheme =
  | 'sunrise'
  | 'daylight'
  | 'noon'
  | 'golden'
  | 'meadow'
  | 'dusk'
  | 'twilight'
  | 'aurora'
  | 'midnight'
  | 'rem';

export type SchemeOrSystem = Scheme | 'system';

export type SchemeMeta = {
  id: Scheme;
  label: string;
  // Each scheme has both modes; this is the scheme's *natural* mood —
  // sunrise / daylight feel "light by default", midnight / rem feel
  // "dark by default" — used to set picker grouping and order.
  defaultMood: Mode;
  icon: LucideIcon;
};

// Order here drives swatch order in settings/theme.tsx. Light-mood
// schemes first (sunrise → noon arc) then dark-mood schemes
// (dusk → rem arc), so the picker reads like a wall-clock day.
export const SCHEMES: readonly SchemeMeta[] = [
  { id: 'sunrise', label: 'Sunrise', defaultMood: 'light', icon: Sunrise },
  { id: 'daylight', label: 'Daylight', defaultMood: 'light', icon: Sun },
  { id: 'noon', label: 'Noon', defaultMood: 'light', icon: SunDim },
  { id: 'golden', label: 'Golden hour', defaultMood: 'light', icon: Sunset },
  { id: 'meadow', label: 'Meadow', defaultMood: 'light', icon: Flower },
  { id: 'dusk', label: 'Dusk', defaultMood: 'dark', icon: CloudSun },
  { id: 'twilight', label: 'Twilight', defaultMood: 'dark', icon: MoonStar },
  { id: 'aurora', label: 'Aurora', defaultMood: 'dark', icon: Sparkles },
  { id: 'midnight', label: 'Midnight', defaultMood: 'dark', icon: Moon },
  // BrainCircuit is used in §4.5 but isn't exported by lucide-react-native;
  // BrainCog is the closest match in the lib's current export set.
  { id: 'rem', label: 'REM', defaultMood: 'dark', icon: BrainCog },
] as const;

const SCHEME_BY_ID = new Map<Scheme, SchemeMeta>(SCHEMES.map((s) => [s.id, s]));

export function schemeById(id: Scheme): SchemeMeta {
  const meta = SCHEME_BY_ID.get(id);
  if (!meta) {
    throw new Error(`unknown scheme: ${id}`);
  }
  return meta;
}

// `system` resolves to daylight in light mode and midnight in dark mode
// — sensible defaults for users who haven't explored the picker. Mode
// itself is independent and always tracks OS Appearance.
export function resolveScheme(selected: SchemeOrSystem, osMode: Mode): Scheme {
  if (selected === 'system') {
    return osMode === 'dark' ? 'midnight' : 'daylight';
  }
  return selected;
}

export const DEFAULT_SCHEME: SchemeOrSystem = 'system';

// ComponentType<unknown> is what the swatch grid actually consumes —
// keeping the icon type one level looser than LucideIcon avoids deep
// generic instantiation when the icons are passed through useCssElement.
export type SchemeIcon = ComponentType<{ size?: number; color?: string }>;
