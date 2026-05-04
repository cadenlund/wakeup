// ESLint 9 flat config. Built on the canonical Expo preset (which includes
// React, React Hooks, TypeScript, and Expo-specific rules) and layered with
// Prettier so format violations surface as ESLint errors and don't drift.
//
// CodeRabbit-friendly notes:
//   - eslint-config-prettier is appended LAST so it disables every formatting
//     rule the Expo preset turns on; eslint-plugin-prettier then re-enables
//     a single `prettier/prettier` rule that runs Prettier as a check.
//   - WAKEUPEXPO.md mandates blocking RN's <Image> and <FlatList> in favor of
//     expo-image and @shopify/flash-list (§1.11, §1.12). Those rules ride in
//     once the Phase 1 milestones land — left as TODOs here so the project
//     compiles cleanly on the Phase 0 boilerplate.
const expoConfig = require("eslint-config-expo/flat");
const prettier = require("eslint-config-prettier");
const prettierPlugin = require("eslint-plugin-prettier");

module.exports = [
  ...expoConfig,
  prettier,
  {
    plugins: { prettier: prettierPlugin },
    rules: {
      "prettier/prettier": "error",
    },
  },
  {
    files: ["**/__tests__/**", "**/*.test.{ts,tsx,js,jsx}"],
    languageOptions: {
      globals: {
        describe: "readonly",
        it: "readonly",
        test: "readonly",
        expect: "readonly",
        beforeAll: "readonly",
        beforeEach: "readonly",
        afterAll: "readonly",
        afterEach: "readonly",
        jest: "readonly",
      },
    },
  },
  {
    ignores: [
      "node_modules/**",
      ".expo/**",
      "dist/**",
      "web-build/**",
      "ios/**",
      "android/**",
      "expo-env.d.ts",
      "lib/api/schema.ts",
      "lib/api/hooks/**",
    ],
  },
];
