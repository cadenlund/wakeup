// Scheme registry — the ten sleep-cycle schemes from WAKEUPEXPO.md §4.5
// plus the `system` pseudo-scheme that follows OS dark/light mode.
//
// The Tailwind tokens themselves live in global.css; this module is the
// JS-side metadata used by the picker UI (settings/theme.tsx, lands in
// milestone 10.x) and the provider's theme resolution.

import type { ComponentType } from "react";
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
} from "lucide-react-native";

export type Mode = "light" | "dark";

export type Scheme =
  | "sunrise"
  | "daylight"
  | "noon"
  | "golden"
  | "meadow"
  | "dusk"
  | "twilight"
  | "aurora"
  | "midnight"
  | "rem";

export type SchemeOrSystem = Scheme | "system";

export type SchemeMeta = {
  id: Scheme;
  label: string;
  mode: Mode;
  icon: LucideIcon;
};

// Order here drives swatch order in settings/theme.tsx. Light first
// (sunrise → noon arc) then dark (dusk → rem arc), so the picker reads
// like a wall-clock day.
export const SCHEMES: readonly SchemeMeta[] = [
  { id: "sunrise", label: "Sunrise", mode: "light", icon: Sunrise },
  { id: "daylight", label: "Daylight", mode: "light", icon: Sun },
  { id: "noon", label: "Noon", mode: "light", icon: SunDim },
  { id: "golden", label: "Golden hour", mode: "light", icon: Sunset },
  { id: "meadow", label: "Meadow", mode: "light", icon: Flower },
  { id: "dusk", label: "Dusk", mode: "dark", icon: CloudSun },
  { id: "twilight", label: "Twilight", mode: "dark", icon: MoonStar },
  { id: "aurora", label: "Aurora", mode: "dark", icon: Sparkles },
  { id: "midnight", label: "Midnight", mode: "dark", icon: Moon },
  // BrainCircuit is used in §4.5 but isn't exported by lucide-react-native;
  // BrainCog is the closest match in the lib's current export set.
  { id: "rem", label: "REM", mode: "dark", icon: BrainCog },
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
// per §4.5. Callers pass the current Appearance.getColorScheme() value.
export function resolveScheme(selected: SchemeOrSystem, osMode: Mode): Scheme {
  if (selected === "system") {
    return osMode === "dark" ? "midnight" : "daylight";
  }
  return selected;
}

// Stable sentinel used as the AsyncStorage key — never change without
// also writing a migration.
export const STORAGE_KEY = "theme:scheme";

export const DEFAULT_SCHEME: SchemeOrSystem = "system";

// ComponentType<unknown> is what the swatch grid actually consumes —
// keeping the icon type one level looser than LucideIcon avoids deep
// generic instantiation when the icons are passed through useCssElement.
export type SchemeIcon = ComponentType<{ size?: number; color?: string }>;
