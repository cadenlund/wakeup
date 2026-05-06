// Full-screen blocking modal per spec §4.10. The backend's
// /v1/healthz returns `min_client_version`; if the running app
// reports an older version we render a non-dismissable gate that
// links to the store. `Updates.fetchUpdateAsync()` runs as a
// courtesy — sometimes an EAS Update can satisfy the bump without a
// store roundtrip.
//
// Polling cadence: every 60s while foregrounded. The query is the
// only auth-less call we make repeatedly, so it doesn't burn auth
// budget.
import Constants from 'expo-constants';
import * as Linking from 'expo-linking';
import * as Updates from 'expo-updates';
import * as React from 'react';
import { Platform, View } from 'react-native';

import { Button } from '@/components/ui/button';
import { Text } from '@/components/ui/text';
import { useGetV1Healthz } from '@/lib/api/hooks/system/system';
import { Sentry } from '@/lib/sentry';

const POLL_INTERVAL_MS = 60_000;
// TODO(release): replace placeholder iOS App ID before any prod build
// that could trip the gate. The string `id000000000` makes a 404 on
// the App Store. While the placeholder is in place we treat the iOS
// gate as DISABLED so blocked iOS users aren't sent to a dead URL —
// the gate logic below special-cases iOS + placeholder and returns
// `children` without rendering the modal. Sentry-capture in that
// branch surfaces a "we'd have blocked but skipped" warning so we
// catch shipping past the placeholder by accident.
const IOS_STORE_URL = 'https://apps.apple.com/app/id000000000';
const IOS_STORE_URL_IS_PLACEHOLDER = IOS_STORE_URL.includes('id000000000');
const ANDROID_STORE_URL = 'https://play.google.com/store/apps/details?id=app.wakeup.client';

// Compares dotted-decimal version strings. Returns true when `min`
// is strictly higher than `current`. Defensive against missing
// segments — `1.2` < `1.2.1`.
export function isUpgradeRequired(current: string | undefined, min: string | undefined): boolean {
  if (!min || !current) return false;
  const cur = current.split('.').map((p) => parseInt(p, 10) || 0);
  const req = min.split('.').map((p) => parseInt(p, 10) || 0);
  const len = Math.max(cur.length, req.length);
  for (let i = 0; i < len; i++) {
    const c = cur[i] ?? 0;
    const r = req[i] ?? 0;
    if (r > c) return true;
    if (r < c) return false;
  }
  return false;
}

export function ForceUpgradeGate({ children }: { children: React.ReactNode }) {
  const { data } = useGetV1Healthz({
    query: {
      refetchInterval: POLL_INTERVAL_MS,
      refetchOnWindowFocus: true,
    },
  });

  // Orval types the response as the wrapped `{data, status, headers}`
  // envelope, but `apiFetch` returns the unwrapped body — every other
  // call site in the app casts. Without this the gate read
  // `data?.data?.min_client_version` which is forever undefined,
  // silently disabling the §4.10 force-upgrade safety check.
  const me = data as { min_client_version?: string } | undefined;
  const minVersion = me?.min_client_version;
  const currentVersion = Constants.expoConfig?.version;
  const wouldBlock = isUpgradeRequired(currentVersion, minVersion);
  // iOS gate is disabled while IOS_STORE_URL is the placeholder —
  // see the constant comment. Surface to Sentry in the would-have-
  // blocked branch so a missed real-URL wire-up doesn't ship silently.
  const iosPlaceholderSkip = wouldBlock && Platform.OS === 'ios' && IOS_STORE_URL_IS_PLACEHOLDER;
  const blocked = wouldBlock && !iosPlaceholderSkip;

  React.useEffect(() => {
    if (iosPlaceholderSkip) {
      Sentry.captureMessage(
        'force-upgrade gate would have blocked iOS but the App Store URL is a placeholder',
        { level: 'warning', tags: { surface: 'force-upgrade-gate' } }
      );
    }
  }, [iosPlaceholderSkip]);

  React.useEffect(() => {
    if (blocked) {
      void Updates.fetchUpdateAsync().catch(() => {
        // EAS Update failure is fine — the user still has the
        // store-link path forward.
      });
    }
  }, [blocked]);

  if (!blocked) return <>{children}</>;

  const storeUrl = Platform.OS === 'ios' ? IOS_STORE_URL : ANDROID_STORE_URL;

  return (
    <View className="flex-1 items-center justify-center gap-4 bg-background px-8">
      <Text variant="h2" className="text-center">
        Update required
      </Text>
      <Text variant="muted" className="text-center">
        Please update from the {Platform.OS === 'ios' ? 'App Store' : 'Play Store'} to keep using
        Wakeup.
      </Text>
      <Button onPress={() => void Linking.openURL(storeUrl)}>
        <Text>Open store</Text>
      </Button>
    </View>
  );
}
