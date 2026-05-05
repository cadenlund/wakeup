// Resolve a theme token to a literal `hsl(...)` color string. Usable
// anywhere a hex/rgb/hsl is required — lucide-react-native's `color`
// prop, react-native-svg fills, Reanimated interpolations.
//
// `hsl(var(--primary))` doesn't parse on native because CSS variables
// only resolve under a browser's CSS engine. On web NW already
// rewrites the var to the active palette via the data-theme cascade.
// This hook reads the same palette table directly so native + web
// converge on the same colour without going through CSS.
import { PALETTES, type Palette } from '@/lib/theme/palettes';
import { useThemeStore } from '@/lib/theme/store';

export type ColorToken = keyof Palette;

export function useThemeColor(token: ColorToken): string {
  const effective = useThemeStore((s) => s.effective);
  const mode = useThemeStore((s) => s.mode);
  return `hsl(${PALETTES[effective][mode][token]})`;
}
