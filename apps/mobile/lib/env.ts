// Centralised env access. Every `EXPO_PUBLIC_*` Metro inlines at
// build time, so reading once here keeps the string literals out of
// hot paths and makes "is this set?" failures loud at startup
// instead of at first request.
//
// Profiles map to eas.json: development → localhost:8080, preview →
// staging.api.wakeup.app, production → api.wakeup.app. Local
// `bunx expo start` (no profile) falls back to localhost so the
// dev-client just works against `just backend-dev`.

const FALLBACK_API_BASE_URL = 'http://localhost:8080';
const FALLBACK_WS_BASE_URL = 'ws://localhost:8080';

export const API_BASE_URL = process.env.EXPO_PUBLIC_API_BASE_URL ?? FALLBACK_API_BASE_URL;

export const WS_BASE_URL = process.env.EXPO_PUBLIC_WS_BASE_URL ?? FALLBACK_WS_BASE_URL;

export const ENV = (process.env.EXPO_PUBLIC_ENV ?? 'development') as
  | 'development'
  | 'preview'
  | 'production';
