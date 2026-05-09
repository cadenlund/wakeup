// Central registry of every AsyncStorage / SecureStore key. Stops
// the pattern of typing `'onboarding:complete'` in three places and
// one of them being a typo.
//
// Conventions:
//   - `as const` so consumers get the exact string literal type, not
//     `string` — useful when a downstream API discriminates on key.
//   - Prefix groups: `theme:*`, `auth:*`, `query:*`, `mutation:*`,
//     `network:*`, etc. Mirrors the §9.1 storage table in
//     docs/WAKEUPEXPO.md.
//   - Bump the `:vN` suffix when the schema breaks (existing entries
//     get re-fetched on the new key, old key sits orphaned until
//     gc — that's fine, AsyncStorage is small).
//
// Anything reading or writing AsyncStorage / SecureStore should
// import its key from here. Bare string literals to AsyncStorage
// are a lint-style smell (we don't enforce yet, but treat them as
// if a CR comment is coming).
export const STORAGE_KEYS = {
  // Theme — see lib/theme/store.ts
  themeScheme: 'theme:scheme',
  themeModePreference: 'theme:mode_preference',

  // TanStack Query persistence — see lib/api/query-client.ts
  queryCache: 'query-cache:v1',
  mutationCache: 'mutation-cache:v1',

  // UI state — see app/(tabs)/_layout.web.tsx (web sidebar collapsed
  // pref, persisted via window.localStorage on web only).
  uiSidebarCollapsed: 'ui:sidebar_collapsed',
} as const;

export type StorageKey = (typeof STORAGE_KEYS)[keyof typeof STORAGE_KEYS];
