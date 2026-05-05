// Zustand store for the active sleep-cycle scheme + light/dark mode.
//
// State machine:
//   - `selected` is what the user picked in the scheme picker (one of
//     the 10 schemes, or `system`). Persisted across launches.
//   - `modePreference` is the user's light/dark override: `light`,
//     `dark`, or `system` (follow OS Appearance). Persisted.
//   - `osMode` mirrors `Appearance.getColorScheme()` so `system` mode
//     can resolve without re-reading Appearance every render. Provider
//     keeps it in sync via the Appearance change listener.
//   - `effective` is the resolved scheme (`Scheme`, never `"system"`)
//     used by the data-theme attribute on the root view. Derived from
//     selected + the resolved mode.
//   - `mode` is the active light/dark mode the rest of the app reads.
//     Derived from modePreference + osMode.
//
// AsyncStorage I/O is best-effort — a failed read just falls back to
// defaults, a failed write logs and continues. The user's pick
// surviving a relaunch is nice-to-have, not a correctness invariant.
// Backend sync (so theme follows the user across devices) lands at
// Phase 2 alongside auth — until then this is a device-local store.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { create } from 'zustand';

import {
  DEFAULT_SCHEME,
  resolveScheme,
  type Mode,
  type Scheme,
  type SchemeOrSystem,
} from '@/lib/theme/schemes';
import { STORAGE_KEYS } from '@/lib/storage-keys';

const DEFAULT_MODE_PREFERENCE: ModePreference = 'system';

export type ModePreference = 'light' | 'dark' | 'system';

type ThemeState = {
  selected: SchemeOrSystem;
  modePreference: ModePreference;
  osMode: Mode;
  effective: Scheme;
  mode: Mode;
  hydrated: boolean;
  setScheme: (scheme: SchemeOrSystem) => Promise<void>;
  setModePreference: (pref: ModePreference) => Promise<void>;
  setOsMode: (mode: Mode) => void;
  hydrateFromStorage: () => Promise<void>;
};

function isSchemeOrSystem(value: unknown): value is SchemeOrSystem {
  return (
    value === 'system' ||
    value === 'sunrise' ||
    value === 'daylight' ||
    value === 'noon' ||
    value === 'golden' ||
    value === 'meadow' ||
    value === 'dusk' ||
    value === 'twilight' ||
    value === 'aurora' ||
    value === 'midnight' ||
    value === 'rem'
  );
}

function isModePreference(value: unknown): value is ModePreference {
  return value === 'light' || value === 'dark' || value === 'system';
}

// Resolve mode from preference + OS signal. `system` follows the OS;
// `light` / `dark` override regardless of OS Appearance.
function resolveMode(pref: ModePreference, os: Mode): Mode {
  return pref === 'system' ? os : pref;
}

export const useThemeStore = create<ThemeState>()((set, get) => ({
  selected: DEFAULT_SCHEME,
  modePreference: DEFAULT_MODE_PREFERENCE,
  osMode: 'light',
  effective: resolveScheme(DEFAULT_SCHEME, 'light'),
  mode: 'light',
  hydrated: false,

  setScheme: async (scheme) => {
    set((s) => ({
      selected: scheme,
      effective: resolveScheme(scheme, s.mode),
    }));
    try {
      await AsyncStorage.setItem(STORAGE_KEYS.themeScheme, scheme);
    } catch (err) {
      // Persistence failure is non-fatal — the picker keeps the new
      // scheme for the rest of this session; next launch falls back
      // to DEFAULT_SCHEME.
      console.warn('theme: failed to persist scheme', err);
    }
  },

  setModePreference: async (pref) => {
    set((s) => {
      const mode = resolveMode(pref, s.osMode);
      return {
        modePreference: pref,
        mode,
        effective: resolveScheme(s.selected, mode),
      };
    });
    try {
      await AsyncStorage.setItem(STORAGE_KEYS.themeModePreference, pref);
    } catch (err) {
      console.warn('theme: failed to persist mode preference', err);
    }
  },

  setOsMode: (osMode) => {
    set((s) => {
      const mode = resolveMode(s.modePreference, osMode);
      return {
        osMode,
        mode,
        effective: resolveScheme(s.selected, mode),
      };
    });
  },

  hydrateFromStorage: async () => {
    if (get().hydrated) return;
    try {
      const [storedScheme, storedMode] = await Promise.all([
        AsyncStorage.getItem(STORAGE_KEYS.themeScheme),
        AsyncStorage.getItem(STORAGE_KEYS.themeModePreference),
      ]);
      set((s) => {
        const selected = isSchemeOrSystem(storedScheme) ? storedScheme : s.selected;
        const modePreference = isModePreference(storedMode) ? storedMode : s.modePreference;
        const mode = resolveMode(modePreference, s.osMode);
        return {
          selected,
          modePreference,
          mode,
          effective: resolveScheme(selected, mode),
          hydrated: true,
        };
      });
    } catch (err) {
      console.warn('theme: failed to read theme from storage', err);
      set({ hydrated: true });
    }
  },
}));
