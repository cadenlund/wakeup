// Phase 8.1 — Expo push-token registration.
//
// `registerForPushNotificationsAsync` is the entry point: it runs once
// the user is authenticated (called from <PushNotifications/>'s bridge
// hook). It:
//
//   1. bails on web / simulators / when notification permission is
//      denied — push is a best-effort enhancement, never a hard
//      dependency, and a denied prompt must not break startup;
//   2. mints an Expo push token (`getExpoPushTokenAsync`, scoped to the
//      EAS projectId so Expo's relay can map it to APNs/FCM);
//   3. POSTs it to `/v1/devices` (idempotent server-side on the
//      (user_id, expo_token) pair, so re-posting an unchanged token is
//      harmless — but we skip the round-trip when the cached token
//      already matches);
//   4. caches `{ expoToken, deviceId }` under `device:registered` so we
//      can (a) skip step 3 next launch and (b) DELETE the right row on
//      logout.
//
// `deregisterPushAsync` is the logout counterpart — it must run while
// the session cookie is still valid (see header-logout-pill's onMutate),
// otherwise the DELETE 401s and the stale row would keep receiving
// pushes for the signed-out user on this device.
//
// Token retrieval can be slow even on real devices (Expo FAQ), so this
// is always fire-and-forget off the startup path — never awaited by a
// render.
import Constants from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';

import { deleteV1DevicesId, postV1Devices } from '@/lib/api/hooks/devices/devices';
import type { InternalHandlerHttpDeviceTokenResponse } from '@/lib/api/model';
import { STORAGE_KEYS } from '@/lib/storage-keys';

const REGISTERED_KEY = STORAGE_KEYS.deviceRegistered;
const ANDROID_CHANNEL_ID = 'default';

type RegisteredCache = { expoToken: string; deviceId: string };

// platform string the backend's RegisterDeviceRequest accepts. Web is
// never reached (we bail above) so 'android' | 'ios' covers it.
function devicePlatform(): 'ios' | 'android' | null {
  return Platform.OS === 'ios' ? 'ios' : Platform.OS === 'android' ? 'android' : null;
}

function easProjectId(): string | undefined {
  // expo-constants surfaces the projectId from app.json's
  // extra.eas.projectId (written by `eas init`). Without it
  // getExpoPushTokenAsync can't resolve which push credential to use.
  const id = Constants.expoConfig?.extra?.eas?.projectId;
  return typeof id === 'string' ? id : undefined;
}

async function readCache(): Promise<RegisteredCache | null> {
  try {
    const raw = await AsyncStorage.getItem(REGISTERED_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<RegisteredCache>;
    if (typeof parsed.expoToken === 'string' && typeof parsed.deviceId === 'string') {
      return { expoToken: parsed.expoToken, deviceId: parsed.deviceId };
    }
  } catch {
    // Corrupt cache → treat as unregistered; we'll re-register.
  }
  return null;
}

// setupAndroidNotificationChannelAsync registers the default channel.
// Android 8+ drops any notification posted to a non-existent channel,
// so this must run before the first push arrives. No-op off Android.
export async function setupAndroidNotificationChannelAsync(): Promise<void> {
  if (Platform.OS !== 'android') return;
  try {
    await Notifications.setNotificationChannelAsync(ANDROID_CHANNEL_ID, {
      name: 'Messages',
      importance: Notifications.AndroidImportance.HIGH,
      // null → default device sound + vibration.
      vibrationPattern: [0, 250, 250, 250],
      lightColor: '#FAFAFA',
    });
  } catch (err) {
    console.warn('[push] android channel setup failed', err);
  }
}

export async function registerForPushNotificationsAsync(): Promise<void> {
  // Web push is a different mechanism (service workers) and out of
  // scope; simulators can't mint a real token (getExpoPushTokenAsync
  // never resolves there — Expo issue #37516).
  if (Platform.OS === 'web' || !Device.isDevice) return;

  const platform = devicePlatform();
  if (!platform) return;

  const projectId = easProjectId();
  if (!projectId) {
    console.warn('[push] no EAS projectId — skipping registration (run `eas init`)');
    return;
  }

  try {
    // Ask only if not already determined; never re-prompt a denial.
    const existing = await Notifications.getPermissionsAsync();
    let status = existing.status;
    if (status === Notifications.PermissionStatus.UNDETERMINED) {
      const requested = await Notifications.requestPermissionsAsync();
      status = requested.status;
    }
    if (status !== Notifications.PermissionStatus.GRANTED) {
      // Degrade gracefully — the in-app banners / unread badge still work.
      return;
    }

    const { data: expoToken } = await Notifications.getExpoPushTokenAsync({ projectId });
    if (!expoToken) return;

    const cached = await readCache();
    if (cached?.expoToken === expoToken) return; // already registered, nothing changed.

    // apiFetch resolves to the bare response body; the orval-generated
    // return type is the {data,status} wrapper, so cast (same pattern
    // as use-ensure-direct-conversation).
    const row = (await postV1Devices({ expo_token: expoToken, platform })) as
      | InternalHandlerHttpDeviceTokenResponse
      | undefined;
    const deviceId = row?.id;
    if (typeof deviceId === 'string' && deviceId) {
      await AsyncStorage.setItem(
        REGISTERED_KEY,
        JSON.stringify({ expoToken, deviceId } satisfies RegisteredCache)
      );
    }
  } catch (err) {
    // Best-effort: a failed registration just means no pushes until the
    // next attempt (next cold start). Log per the project's
    // log-swallowed-errors rule.
    console.warn('[push] registration failed', err);
  }
}

// deregisterPushAsync removes this device's token server-side and clears
// the local cache. MUST be called while still authenticated (the DELETE
// needs the session cookie) — header-logout-pill fires it from the
// logout mutation's onMutate, before the logout response clears the
// cookie.
export async function deregisterPushAsync(): Promise<void> {
  const cached = await readCache();
  // Clear the local cache regardless so a re-login re-registers cleanly.
  try {
    await AsyncStorage.removeItem(REGISTERED_KEY);
  } catch {
    // ignore — worst case we re-register an identical row next login.
  }
  if (!cached) return;
  try {
    await deleteV1DevicesId(cached.deviceId);
  } catch (err) {
    // A 401 here means the cookie was already gone — the row stays
    // server-side until the backend prunes it via an Expo
    // DeviceNotRegistered receipt. Anything else: best-effort, log.
    console.warn('[push] deregister failed', err);
  }
}
