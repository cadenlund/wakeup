// Centralised env access. Every `EXPO_PUBLIC_*` Metro inlines at
// build time, so reading once here keeps the string literals out of
// hot paths and makes "is this set?" failures loud at startup
// instead of at first request.
//
// Profiles map to eas.json: development → localhost:8080, preview →
// staging.api.wakeup.app, production → api.wakeup.app. Localhost
// fallback applies ONLY in development — preview/production builds
// throw at module load if the URLs aren't injected, so a misconfigured
// EAS profile fails loudly instead of silently pointing at localhost.
// (CR on PR #115.)

const FALLBACK_API_BASE_URL = 'http://localhost:8080';
const FALLBACK_WS_BASE_URL = 'ws://localhost:8080';

const VALID_ENVS = ['development', 'preview', 'production'] as const;
type Env = (typeof VALID_ENVS)[number];

const rawEnv = process.env.EXPO_PUBLIC_ENV ?? 'development';
if (!VALID_ENVS.includes(rawEnv as Env)) {
  throw new Error(`Invalid EXPO_PUBLIC_ENV: ${rawEnv}. Must be one of: ${VALID_ENVS.join(', ')}.`);
}

export const ENV: Env = rawEnv as Env;

function requireBaseUrl(name: string, value: string | undefined, fallback: string): string {
  if (value) return value;
  if (ENV === 'development') return fallback;
  throw new Error(`Missing required ${name} for EXPO_PUBLIC_ENV=${ENV}.`);
}

export const API_BASE_URL = requireBaseUrl(
  'EXPO_PUBLIC_API_BASE_URL',
  process.env.EXPO_PUBLIC_API_BASE_URL,
  FALLBACK_API_BASE_URL
);

export const WS_BASE_URL = requireBaseUrl(
  'EXPO_PUBLIC_WS_BASE_URL',
  process.env.EXPO_PUBLIC_WS_BASE_URL,
  FALLBACK_WS_BASE_URL
);
