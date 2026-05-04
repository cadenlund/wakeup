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
  // We register className manually on a per-component basis via
  // useCssElement (lib/tw/index.tsx) rather than monkey-patching every
  // RN component. The polyfill would conflict with that pattern.
  globalClassNamePolyfill: false,
});
