// ThemeProvider: keeps the Zustand store in sync with the OS dark/light
// signal and renders a wrapping View whose `data-theme` attribute drives
// the per-scheme @theme overrides in global.css.
//
// The provider mounts once at the root (app/_layout.tsx) and is the
// only place that reads the OS Appearance — every consumer reads the
// resolved scheme via useThemeStore so React re-renders only when the
// effective scheme actually changes.
import * as React from "react";
import { useEffect } from "react";
import { Appearance, type ColorSchemeName } from "react-native";

import { View } from "@/lib/tw";
import { useThemeStore } from "@/lib/theme/store";

function appearanceToMode(scheme: ColorSchemeName): "light" | "dark" {
  return scheme === "dark" ? "dark" : "light";
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const effective = useThemeStore((s) => s.effective);
  const mode = useThemeStore((s) => s.mode);
  const setOsMode = useThemeStore((s) => s.setOsMode);
  const hydrateFromStorage = useThemeStore((s) => s.hydrateFromStorage);

  // Hydrate the persisted scheme on mount. The store starts with
  // DEFAULT_SCHEME so the very first frame has SOMETHING to render
  // against — the hydration just upgrades it to whatever the user
  // picked last session.
  useEffect(() => {
    void hydrateFromStorage();
  }, [hydrateFromStorage]);

  // Mirror OS Appearance into the store so `system` resolves correctly
  // and live-updates on Settings → Display & Brightness changes.
  useEffect(() => {
    setOsMode(appearanceToMode(Appearance.getColorScheme()));
    const sub = Appearance.addChangeListener(({ colorScheme }) => {
      setOsMode(appearanceToMode(colorScheme));
    });
    return () => sub.remove();
  }, [setOsMode]);

  // Both attributes drive the global.css selectors:
  //   - [data-theme="<scheme>"] picks the color personality
  //   - [data-mode="light"|"dark"] picks the light/dark variant
  // Combined: [data-theme="midnight"][data-mode="dark"] { … } etc.
  // The same data-mode attribute also drives Tailwind's `dark:`
  // variant via the `@variant dark (…)` declaration in global.css —
  // so RNR's `dark:bg-destructive/60` etc. fires off the user's
  // chosen scheme, not the OS Appearance signal alone.
  return (
    <View data-theme={effective} data-mode={mode} className="flex-1 bg-background">
      {children}
    </View>
  );
}
