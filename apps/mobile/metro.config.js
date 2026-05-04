// Metro is wrapped with NativeWind v5 + Tailwind v4 via the
// `react-native-css` runtime. Configuration is CSS-first per the
// `expo:expo-tailwind-setup` skill — no babel.config.js for NativeWind
// needed in v5; the className-to-style transform happens in Metro.
const { getDefaultConfig } = require("expo/metro-config");
const { withNativewind } = require("nativewind/metro");

/** @type {import('expo/metro-config').MetroConfig} */
const config = getDefaultConfig(__dirname);

module.exports = withNativewind(config, {
  // Inlining variables into the runtime breaks PlatformColor in CSS
  // variables — the skill calls this out explicitly. We rely on
  // platformColor() inside @media ios blocks for native-system color
  // tokens, so this MUST stay false.
  inlineVariables: false,
  // The polyfill makes `className` work on every bare RN primitive
  // (View/Text/Pressable/etc.) without per-component useCssElement
  // wiring. We need this on because react-native-reusables ships
  // ~30 components built against bare `<RNText className=...>` —
  // turning the polyfill off would mean rewriting every RNR file
  // on every CLI re-add. lib/tw's useCssElement wrappers still work
  // alongside the polyfill (they're just per-instance overrides),
  // but everyday code can use bare RN components with className.
  globalClassNamePolyfill: true,
});
