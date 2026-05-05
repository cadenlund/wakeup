// Sentry init per spec §4.10. Imported (not just `import`-side-effect)
// from `app/_layout.tsx` so `Sentry.init()` runs at module load — that
// happens before any React tree mounts, which is required for the
// React-Navigation/Expo-Router integration to attach correctly.
//
// Spec policy:
//   - DSN from EXPO_PUBLIC_SENTRY_DSN (set per profile in eas.json).
//     Empty string disables Sentry entirely — no-op for local dev
//     where the operator hasn't pasted a DSN yet.
//   - environment from EXPO_PUBLIC_ENV (eas.json profile name).
//   - release = `${appVersion}+${runtimeVersion}` so EAS Update
//     bundles deploying onto the same native build show as separate
//     releases.
//   - tracesSampleRate: 1.0 in dev, 0.1 in prod (per §4.10 line 453).
//   - beforeSend strips request bodies and any property named
//     `email`, `password`, or `token` so PII never leaves the device.
//   - We don't ship Expo Go (per project memory), so no isRunningIn-
//     ExpoGo guards — native-only features are always available.
import * as Sentry from '@sentry/react-native';
import Constants from 'expo-constants';

import { ENV, SENTRY_DSN } from '@/lib/env';

export const navigationIntegration = Sentry.reactNavigationIntegration({
  enableTimeToInitialDisplay: true,
});

const SENSITIVE_KEYS = new Set(['email', 'password', 'token']);

// Recursive PII scrub. Mutates a shallow clone so we don't disturb
// the original event Sentry hands us.
function scrubPII<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((v) => scrubPII(v)) as unknown as T;
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) {
      if (SENSITIVE_KEYS.has(k)) continue;
      out[k] = scrubPII(v);
    }
    return out as T;
  }
  return value;
}

const isDev = ENV === 'development';

const appVersion = Constants.expoConfig?.version ?? '0.0.0';
const runtimeVersion =
  typeof Constants.expoConfig?.runtimeVersion === 'string'
    ? Constants.expoConfig.runtimeVersion
    : 'unset';

export const sentryEnabled = !!SENTRY_DSN;

if (sentryEnabled) {
  Sentry.init({
    dsn: SENTRY_DSN,
    environment: ENV,
    release: `${appVersion}+${runtimeVersion}`,
    tracesSampleRate: isDev ? 1.0 : 0.1,
    integrations: [navigationIntegration],
    enableNativeFramesTracking: true,
    beforeSend(event) {
      // Drop request bodies wholesale — they're the most common
      // vector for credentials, free-text DMs, and idempotency keys.
      if (event.request) {
        delete event.request.data;
      }
      return scrubPII(event);
    },
  });
}

export { Sentry };
