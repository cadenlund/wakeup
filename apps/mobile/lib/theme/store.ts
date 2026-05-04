// Zustand store for the active sleep-cycle scheme + mode.
//
// State machine:
//   - `selected` is what the user picked in settings/theme.tsx (one of
//     the 10 schemes, or `system`). Persisted across launches.
//   - `osMode` mirrors `Appearance.getColorScheme()` so `system` can
//     resolve to daylight (light) or midnight (dark) without re-reading
//     the system every render. The provider keeps this in sync via the
//     Appearance change listener.
//   - `effective` is the resolved scheme (`Scheme`, never `"system"`)
//     used by the data-theme attribute on the root view. Derived from
//     selected + osMode.
//   - `mode` is the active light/dark mode used by the data-mode
//     attribute. Currently mirrors osMode 1:1; a future iteration can
//     surface a manual override (Settings → "Always dark", etc.).
//
// AsyncStorage I/O is best-effort — a failed read just falls back to
// DEFAULT_SCHEME, a failed write logs and continues. The user's pick
// surviving a relaunch is nice-to-have, not a correctness invariant.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { create } from "zustand";

import {
  DEFAULT_SCHEME,
  STORAGE_KEY,
  resolveScheme,
  type Mode,
  type Scheme,
  type SchemeOrSystem,
} from "@/lib/theme/schemes";

type ThemeState = {
  selected: SchemeOrSystem;
  osMode: Mode;
  effective: Scheme;
  mode: Mode;
  hydrated: boolean;
  setScheme: (scheme: SchemeOrSystem) => Promise<void>;
  setOsMode: (mode: Mode) => void;
  hydrateFromStorage: () => Promise<void>;
};

function isSchemeOrSystem(value: unknown): value is SchemeOrSystem {
  return (
    value === "system" ||
    value === "sunrise" ||
    value === "daylight" ||
    value === "noon" ||
    value === "golden" ||
    value === "meadow" ||
    value === "dusk" ||
    value === "twilight" ||
    value === "aurora" ||
    value === "midnight" ||
    value === "rem"
  );
}

export const useThemeStore = create<ThemeState>()((set, get) => ({
  selected: DEFAULT_SCHEME,
  osMode: "light",
  effective: resolveScheme(DEFAULT_SCHEME, "light"),
  mode: "light",
  hydrated: false,

  setScheme: async (scheme) => {
    set((s) => ({
      selected: scheme,
      effective: resolveScheme(scheme, s.osMode),
    }));
    try {
      await AsyncStorage.setItem(STORAGE_KEY, scheme);
    } catch (err) {
      // Persistence failure is non-fatal — the picker keeps the new
      // scheme for the rest of this session; next launch falls back
      // to DEFAULT_SCHEME.
      console.warn("theme: failed to persist scheme", err);
    }
  },

  setOsMode: (mode) => {
    set((s) => ({
      osMode: mode,
      mode,
      effective: resolveScheme(s.selected, mode),
    }));
  },

  hydrateFromStorage: async () => {
    if (get().hydrated) return;
    try {
      const stored = await AsyncStorage.getItem(STORAGE_KEY);
      if (stored != null && isSchemeOrSystem(stored)) {
        set((s) => ({
          selected: stored,
          effective: resolveScheme(stored, s.osMode),
          hydrated: true,
        }));
        return;
      }
    } catch (err) {
      console.warn("theme: failed to read scheme from storage", err);
    }
    set({ hydrated: true });
  },
}));
