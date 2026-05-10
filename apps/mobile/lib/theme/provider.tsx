// ThemeProvider: drives the active sleep-cycle scheme + light/dark
// mode by injecting CSS variables into the React tree via NativeWind's
// `vars()` helper.
//
// Why `vars()` instead of pure CSS selectors: NW v4's compiler reads
// global.css's `[data-theme="..."]:root` blocks for WEB, but the
// native bundle doesn't pick up rules that only define CSS variables
// (no other style props). On native we therefore inject the active
// palette directly. global.css is the web source of truth (browser
// CSS engine handles the cascade); lib/theme/palettes.ts is the
// native source of truth, kept in sync via the schemes.css→palettes
// build step.
//
// Mounts once at the root (app/_layout.tsx). Reads OS Appearance only
// here so consumers re-render only when the effective scheme or mode
// actually changes.
import * as React from 'react';
import { useEffect, useMemo } from 'react';
import { Appearance, View, type ColorSchemeName } from 'react-native';
import { vars } from 'nativewind';

import { PALETTES, paletteToVars } from '@/lib/theme/palettes';
import { useThemeStore } from '@/lib/theme/store';

function appearanceToMode(scheme: ColorSchemeName): 'light' | 'dark' {
  return scheme === 'dark' ? 'dark' : 'light';
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

  // On web, mirror the theme attributes onto <html> so the
  // `[data-theme="..."]:root` selectors in global.css match the
  // document root. Without this the CSS variables only cascade
  // inside the wrapping <View> (a <div>) below, and any content
  // portaled to document.body — RN <Modal> overlays included —
  // resolves bg-card / text-foreground / etc. to the defaults
  // (white) instead of the active scheme. Native is unaffected
  // because there's no DOM to mirror onto.
  useEffect(() => {
    if (typeof document === 'undefined') return;
    const html = document.documentElement;
    html.setAttribute('data-theme', effective);
    html.classList.toggle('dark', mode === 'dark');
  }, [effective, mode]);

  // Resolve the active palette and shape it as `vars()` for native CSS
  // variable injection. Memoised on (effective, mode) so identity is
  // stable between unrelated re-renders — VariableContext consumers
  // skip work when nothing changed.
  const themeStyle = useMemo(
    () => vars(paletteToVars(PALETTES[effective][mode])),
    [effective, mode]
  );

  // `dataSet` is the React Native Web way to emit `data-*` HTML
  // attributes on the rendered DOM element; on web the
  // `[data-theme="..."]` selectors in global.css match against this
  // and the browser's CSS engine handles the cascade. On native the
  // `style={themeStyle}` injection above is the load-bearing path.
  // `dataSet` is typed only on RN Web's View — RN's index.d.ts omits
  // it. Cast through unknown so the prop is accepted on both.
  const dataAttrs = {
    dataSet: { theme: effective },
  } as Record<string, unknown>;

  const className = mode === 'dark' ? 'dark flex-1 bg-background' : 'flex-1 bg-background';

  return (
    <View {...dataAttrs} style={themeStyle} className={className}>
      {children}
    </View>
  );
}
