/* eslint-env node */
const { defineConfig } = require('eslint/config');
const expoConfig = require('eslint-config-expo/flat');

module.exports = defineConfig([
  expoConfig,
  {
    // `lib/api/schema.ts`, `lib/api/hooks/`, and `lib/api/model/` are
    // emitted by `just gen-client` (openapi-typescript + orval). Don't
    // lint generated output — fixing the generator's style nits would
    // be undone on every regen.
    ignores: ['dist/*', 'lib/api/schema.ts', 'lib/api/hooks/**', 'lib/api/model/**'],
  },
  {
    rules: {
      'react/display-name': 'off',
      // Per WAKEUPEXPO §4.9: every image renders through `expo-image`,
      // never RN's stock `<Image>`. expo-image's disk cache, blurhash
      // placeholders, and AVIF/WebP decoding are the load-bearing
      // wins. This rule fails any code path that imports `Image`
      // (default or named) from react-native.
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: 'react-native',
              importNames: ['Image', 'ImageBackground'],
              message: "Use `Image` from 'expo-image' instead (per WAKEUPEXPO §4.9).",
            },
            {
              name: 'react-native',
              importNames: ['FlatList', 'SectionList', 'VirtualizedList'],
              message:
                "Use `List` from '@/components/ui/list' (FlashList) instead (per WAKEUPEXPO §4.9).",
            },
          ],
        },
      ],
    },
  },
]);
