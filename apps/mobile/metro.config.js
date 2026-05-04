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
  // Polyfill ON so every react-native primitive (View, Text, Pressable,
  // ...) accepts a className prop directly. Required for the
  // react-native-reusables foundation (§3.1) — RNR components ship
  // importing primitives from react-native and applying className on
  // them. The lib/tw/ wrappers stay around for the cases where the
  // wrapper adds value (TouchableHighlight underlayColor extraction,
  // AnimatedScrollView dual-class handling) but are no longer
  // load-bearing for className resolution.
  globalClassNamePolyfill: true,
});
