# Wakeup — Expo client spec (locked)

The mobile companion to the Go backend documented in `docs/WAKEUP.md`. Read both — this doc assumes you've absorbed the backend's API surface, WebSocket protocol, and security model.

This spec is the single source of truth for the Expo client. It is locked the same way `WAKEUP.md` is locked: every choice in §3 is final unless explicitly negotiated. Implementation flows top-to-bottom through §16 (the checklist).

---

## 0. Rules of engagement (read first, every time)

These rules are non-negotiable. If a step requires a question, ask the human operator — do not invent answers.

1. **Work strictly top-to-bottom through §16 (the checklist).** No phase begins until the prior phase is fully checked off, committed, CI-green, and CodeRabbit feedback resolved.
2. **One commit per checked-off milestone.** Use the commit message specified for that milestone. Conventional Commits format. Author = `cadenlund`, no Claude co-author.
3. **CI must be green before moving on.** If lint, type-check, or test fails, stop and fix before the next milestone. Never `--no-verify` past a hook. Never skip tests.
4. **CodeRabbit feedback is binding.** Same loop as the backend: address every actionable comment, push a follow-up commit, wait for re-review, loop until clean.
5. **Backend spec wins on conflicts.** When `WAKEUP.md` and this doc disagree, the backend spec is canonical — report the inconsistency and ask before diverging client behaviour.
6. **API truth lives in `apps/mobile/lib/api/schema.ts`.** Generated from the backend's OpenAPI via `just gen-client`. Never hand-edit. When the backend changes, regenerate first, then write client code against the new types.
7. **No hand-rolled HTTP fetches against `/v1/*`.** Every API call routes through the Orval-generated hook (or a thin wrapper around it). Bypassing the wrapper bypasses idempotency, auth interception, and toast handling.
8. **Tests-first per layer.** Component snapshot/render → Maestro flow → manual review with the operator. Never ship a screen without a Maestro flow that drives it end-to-end.
8a. **The QR-on-phone review is mandatory at the end of every screen-bearing milestone.** Run `just mobile-tunnel`, post the QR, wait for the operator to scan it on their phone and explicitly approve. "Maestro screenshots looked fine" is not a substitute. See §12.5.
8b. **Use the `expo:*` skills.** The Claude Code session has the Expo plugin installed. Before writing code on any milestone in §16, check the §15.1 cheat-sheet for a relevant skill and invoke it via the `Skill` tool. The skills hold current Expo SDK guidance — your training data does not.
9. **No new dependencies without justification.** Stack is locked in §3. If you genuinely need something not listed, stop and ask.
10. **If you are unsure how a library, API, tool, or protocol works — query the web for the official docs before writing code.** Your training data may be stale. Use WebFetch / WebSearch on the official documentation, GitHub README, or npm. Never invent function signatures, prop shapes, or hook names from memory. Cite the source URL in the commit body when the answer was non-obvious.
11. **Universal across iOS and Android.** Both platforms are first-class for v1. Any code that lands in `*.ios.tsx` or `*.android.tsx` needs an explicit comment justifying the platform split.
12. **STOP and contact Caden if the implementation path is ambiguous.** Specifically:
    - The spec is silent on a behaviour (e.g., "what happens when the WS reconnects mid-call").
    - Two viable component libraries / patterns exist with non-obvious trade-offs.
    - You are about to introduce a pattern not described anywhere in this spec (a new state store, a new auth flow, a new file-naming scheme).
    - You discover the spec contradicts itself or the backend.

    The cost of asking is one round-trip. The cost of guessing wrong is rewriting an entire screen tree. Phrase the question concretely: "Spec §X says A, spec §Y implies B, both reasonable, here are the trade-offs — which?" Wait for an answer. Do NOT continue with both options "until told otherwise."

    Do NOT trigger this rule for: applying a documented pattern to a new screen, adding a new RNR component, writing a Maestro flow against an established screen.
13. **Final acceptance criterion (§17):** every screen in §5 is reachable, every API endpoint in `WAKEUP.md` §6 has at least one client call, every WebSocket event in `WAKEUP.md` §7.2 is handled, and a human can run through `docs/smoke.md` end-to-end on both iOS Simulator and Android Emulator with no surprises.

---

## 1. Product overview

**Wakeup mobile** is a single Expo app targeting iOS, Android, and (incidentally) web — though only iOS + Android are tested for v1. Web works because Expo Router supports it; we don't gate any code on platform.

**Tabs (§5.2):** Conversations · Friends · Profile. **For users with `role === 'admin'` on `GET /v1/auth/me`, a fourth Admin tab is rendered** to the right of Profile (see §5.3).

**Defining UX features for v1:**
- Friend-graph chat (DM + group up to 25). DM auto-creates on first message — tapping a friend in the Friends tab opens (or creates) the 1:1 conversation immediately.
- Discord-style voice/video room per conversation. Voice-by-default; toggle camera mid-call. Picture-in-picture bubble when navigating away.
- Real-time presence dots (online / away / sleeping / dnd / offline). DND is sticky across app backgrounding and suppresses pushes.
- Per-user bio + status emoji on the public profile.
- Mute and pin per conversation. Pinned floats to the top of the list; muted hides push notifications without losing in-app surfaces.
- Email-based contact sync — find friends already on Wakeup without sending raw addresses.
- Push notifications for incoming calls, friend requests, and messages — gated by per-category preferences.
- 10 sleep-cycle-themed colour schemes, picker in profile.
- Optional biometric (Face ID / Touch ID / device passcode) app lock.

**Out of scope for v1:**
- OAuth / social sign-in. Username + password only.
- E2E encryption.
- Threads, reactions, message editing UI (the backend supports edit but we don't expose it client-side until v2).
- True OS-level Picture-in-Picture (the in-app draggable bubble is enough for v1; native PiP on iOS requires a `voip` background mode + a small native module — punt to v2).
- Sleep cycle tracking. The themes are named after sleep stages; there's no actual sleep tracking.

**Differences from the backend's voice in WAKEUP.md §10.3:**
- The backend treats `room.started` as "the call is happening" (first participant joined). The client overlays "Calling friends…" UI on the initiator's screen until the participant count reaches 2 — purely a client-side state, no extra WS event needed.
- The backend issues 10-minute LiveKit JWTs. The client treats `JoinResult.expires_at` as advisory; LiveKit's SDK auto-refreshes during the connection.

---

## 2. App layout (Expo Router file structure)

```
apps/mobile/
├── app.json                      # Expo config (name, slug, plugins, EAS, themes)
├── eas.json                      # EAS Build + EAS Update channels
├── package.json
├── tsconfig.json
├── tailwind.config.ts            # NativeWind v5 theme tokens (10 schemes, see §4.5)
├── global.css                    # @tailwind base + @theme overrides per scheme
├── babel.config.js
├── metro.config.js
├── .maestro/                     # Maestro flow files (.yaml), one per §5.1 screen
├── app/                          # Expo Router file-based routes
│   ├── _layout.tsx               # root: Providers (Query, Theme, BiometricGate, CallOverlay, ToastRoot)
│   ├── (auth)/
│   │   ├── _layout.tsx           # stack, no tab bar
│   │   ├── login.tsx
│   │   ├── register.tsx
│   │   ├── forgot.tsx
│   │   └── reset.tsx             # token-confirm screen for password reset deep link
│   ├── (tabs)/
│   │   ├── _layout.tsx           # tabs config, 3 or 4 icons (admin tab gated by role)
│   │   ├── index.tsx             # → Conversations list
│   │   ├── friends.tsx
│   │   ├── profile.tsx
│   │   └── admin.tsx             # Admin landing — rendered only when role === 'admin'
│   ├── admin/
│   │   ├── users.tsx             # search + list users (active/locked/admin)
│   │   ├── user/[id].tsx         # admin view of one user, edit role/lock/impersonate
│   │   └── audit.tsx             # paginated audit log viewer
│   ├── conversation/
│   │   ├── [id].tsx              # message thread + RoomBanner
│   │   └── new.tsx               # multi-select friends + group name
│   ├── settings/
│   │   ├── _layout.tsx           # modal stack, presented from profile
│   │   ├── account.tsx
│   │   ├── privacy.tsx           # biometric lock toggle, lock-after timeout
│   │   ├── notifications.tsx     # category toggles (matches backend prefs)
│   │   ├── theme.tsx             # 10-swatch picker
│   │   └── devices.tsx           # registered Expo push tokens
│   └── +not-found.tsx
├── lib/
│   ├── api/
│   │   ├── schema.ts             # generated by `just gen-client` (openapi-typescript)
│   │   ├── client.ts             # fetch wrapper (cookies, idempotency, error→toast)
│   │   ├── orval.config.ts       # Orval input → React Query hooks
│   │   ├── hooks/                # generated React Query hooks (one file per tag)
│   │   └── idempotency.ts        # uuid v7 generator + per-request key
│   ├── ws/
│   │   ├── client.ts             # singleton WS connection, exp backoff reconnect
│   │   ├── dispatcher.ts         # event → React Query cache invalidation/mutation
│   │   └── lifecycle.ts          # foreground/background → connect/disconnect
│   ├── call/
│   │   ├── room.ts               # singleton LiveKit Room instance
│   │   ├── store.ts              # Zustand: state machine (idle/dialing/active)
│   │   ├── overlay.tsx           # full-screen + PiP render modes
│   │   └── permissions.ts        # mic/cam pre-flight via expo-camera/expo-audio
│   ├── push/
│   │   ├── register.ts           # expo-notifications setup → POST /v1/devices
│   │   └── handlers.ts           # foreground vs background routing
│   ├── theme/
│   │   ├── schemes.ts            # 10 named schemes + token tables
│   │   ├── store.ts              # Zustand-backed theme state, AsyncStorage persisted
│   │   └── provider.tsx          # NativeWind className resolver
│   ├── biometric/
│   │   ├── gate.tsx              # <BiometricGate> root overlay
│   │   └── store.ts              # toggle + last-unlock timestamp
│   ├── store/                    # other Zustand stores (selected conv, draft text)
│   ├── toast.ts                  # burnt wrapper, conventions from §4.6
│   └── env.ts                    # API_BASE_URL etc, sourced from eas.json profiles
├── components/
│   ├── chat/
│   │   ├── MessageList.tsx       # gifted-chat shell
│   │   ├── ChatBubble.tsx
│   │   └── Composer.tsx
│   ├── call/
│   │   ├── RoomBanner.tsx        # "Join call (3 in room)" / "Alice is calling…"
│   │   ├── ParticipantTile.tsx   # avatar + speaking pulse OR <VideoTrack>
│   │   ├── ControlBar.tsx        # mic/camera/leave
│   │   └── DraggablePip.tsx      # corner-snapping bubble
│   ├── friends/
│   │   ├── FriendRow.tsx
│   │   └── PresenceDot.tsx
│   ├── themed/                   # RNR-installed components (Button, Card, Input, etc.)
│   └── ui/                       # custom one-offs not in RNR
├── widgets/
│   ├── ios/                      # SwiftUI WidgetKit target via @expo/ui/swift-ui
│   └── android/                  # react-native-android-widget JS-defined widget
└── assets/
    ├── icons/
    └── splash/
```

---

## 3. Tech stack (locked)

| Concern | Library / Tool | Why |
|---|---|---|
| Runtime | **Expo SDK 51+** | Locked; tracks the version EAS Hosting publishes |
| Routing | **Expo Router v3** (file-based) | Folder = screen; parallel routes for the call overlay |
| Styling | **NativeWind v5** | Tailwind classes adapted for RN; the `@tailwind base` directive in `global.css` plus per-scheme `@theme` overrides drives 10 schemes |
| Foundation components | **react-native-reusables** (RNR) | shadcn-style copy-in components — see §3.1 below for the full v1 install list, including their auth blocks |
| Server state | **TanStack Query v5** | Caching, retries, optimistic updates, cache invalidation hooks |
| API client codegen | **Orval** + `openapi-typescript` | Orval reads the openapi-typescript schema, emits typed React Query hooks per endpoint |
| Local state | **Zustand** | Tiny stores for theme, selected conv, draft text, call state, biometric toggle |
| Persisted state | **`@react-native-async-storage/async-storage`** + **`expo-secure-store`** | AsyncStorage for non-sensitive prefs; SecureStore (Keychain/Keystore) for anything biometric-locked |
| Chat UI | **`react-native-gifted-chat`** | Bubble + composer; we customise the renderers, not rebuild them |
| Voice/video SDK | **`@livekit/react-native`** + **`@livekit/react-native-webrtc`** | Hooks (`useTracks`, `useParticipants`, `useRoomContext`); custom UI on top |
| Audio routing | **`react-native-incall-manager`** | Speakerphone toggle + proximity sensor for ear-piece mode |
| Native call UI | **`react-native-callkeep`** | iOS CallKit + Android ConnectionService — incoming call rings/displays from the lock screen, audio session integrates with Siri / Bluetooth / car-play. Required for App Store review of any VoIP app |
| Push notifications | **`expo-notifications`** | Token registration + foreground/background handlers |
| Local push categories | **`expo-notifications`** action buttons | Accept/Decline buttons on incoming-call notifications |
| VoIP push (iOS) | **PushKit** via `react-native-callkeep` config | Wakes the app from a fully-killed state for incoming calls. Standard Expo push tokens don't fire when the app is force-quit |
| Biometric | **`expo-local-authentication`** | Face ID / Touch ID / device passcode prompt |
| Toasts | **`burnt`** | Native iOS HUDs + Android material toasts; one library, two looks |
| Haptics | **`expo-haptics`** | Light tap on send / refresh, success notification on friend accept, warning on call decline. See §4.11 |
| Icons | **`lucide-react-native`** | Sleep-themed icons (`Sun`, `Moon`, `Sunrise`, `Sparkles`, etc.) and the rest |
| Image rendering | **`expo-image`** | Disk-cached, format-aware (WebP, AVIF), placeholder + blurhash support. Replaces RN's `<Image>` everywhere — avatars, message attachments, scheme swatches |
| Image compression | **`expo-image-manipulator`** | Resize + JPEG-quality squeeze before `POST /v1/users/me/avatar` (and any future attachment upload). Cap at 1024px / 85% quality client-side so we never push 12MB photos through the API |
| Long lists | **`@shopify/flash-list`** | Replacement for `<FlatList>` on the conversation list AND inside `<MessageList>`. Window recycling — keeps memory flat at 10k+ messages |
| Animations | **`react-native-reanimated`** v3 | RNR + the call PiP bubble both lean on it |
| Gestures | **`react-native-gesture-handler`** | Required by Reanimated + draggable PiP |
| Network state | **`@react-native-community/netinfo`** | Reads online/offline transitions; feeds the offline-queue retry logic in §4.10 |
| Crash + error reporting | **`@sentry/react-native`** | Mirrors the backend's Sentry integration. Same DSN family, separate environments. Captures JS errors, native crashes, and a `routeName` breadcrumb on every Expo Router transition |
| App tracking transparency | **`expo-tracking-transparency`** | Required prompt on iOS 14+ even though we don't track across apps; rejecting it is fine — we just need to ask once or App Store flags us |
| iOS widgets | **`@expo/ui/swift-ui`** (Expo skill) | Real WidgetKit targets authored in SwiftUI, embedded in the Expo app |
| Android widgets | **`react-native-android-widget`** | JS-authored widgets |
| OTA updates | **EAS Update** | JS bundle pushes without store review |
| CI | EAS Build + GitHub Actions | EAS for native builds; Actions for type-check + Maestro |
| Testing — UI flows | **Maestro** + **Maestro MCP** | One flow per screen; the Maestro MCP drives the simulator while the human operator scans the Expo Go QR code on their phone for visual review |
| Live preview | **Expo Go** + `--tunnel` mode | QR code in the terminal opens the app on any device on any network; the operator can preview on phone, tablet, web, and laptop browser simultaneously while the implementer iterates |
| Testing — components | `jest-expo` + `@testing-library/react-native` | Render tests for components that have non-trivial logic (composer state, draggable bubble, etc.) |
| Lint | `eslint-config-expo` + `prettier` | Default Expo lint config; Prettier for format |
| Type-check | TypeScript strict | `tsconfig.json` extends `expo/tsconfig.base` with `"strict": true` |
| Secrets / env | EAS env vars + `.env` file gated by `eas.json` profiles | Three profiles: development, preview, production |

### 3.1 Full RNR install list (locked)

We install every RNR component up front — even ones we don't use day one — because adding them later is one CLI command and the RNR copy-in pattern means each component is just a file in `components/themed/`. The set:

**Components (all of them — they're cheap):**
```
accordion alert alert-dialog aspect-ratio avatar badge button card checkbox
collapsible context-menu dialog dropdown-menu hover-card input label menubar
popover progress radio-group select separator skeleton switch tabs text
textarea toggle toggle-group tooltip
```

Install via:
```
npx @react-native-reusables/cli@latest add accordion alert alert-dialog \
  aspect-ratio avatar badge button card checkbox collapsible context-menu \
  dialog dropdown-menu hover-card input label menubar popover progress \
  radio-group select separator skeleton switch tabs text textarea toggle \
  toggle-group tooltip
```

**Auth blocks (use directly for the auth flow):**
```
sign-in-form sign-up-form forgot-password-form reset-password-form
verify-email-form user-menu
```

We use the prebuilt forms instead of hand-rolling. The "social connections" block is **skipped** — no OAuth in v1 (per §1).

**Why install everything up front:**
- RNR is copy-in, not a runtime dependency, so unused components have zero cost beyond a few KB in the repo.
- Avoids context-switches mid-implementation ("oh, I need a `<Switch>` now, let me run the CLI"). The whole library is local from Phase 1.
- Makes design exploration fast — operator can reference any component when reviewing screens.

---

## 4. Architecture patterns

### 4.1 File-import conventions

- Path alias `@/` maps to `apps/mobile/`. Configured in `tsconfig.json` and `babel.config.js`.
- Screens live in `app/`. Components live in `components/`. Anything stateful (stores, singletons, hooks for non-API state) lives in `lib/`.
- Generated code lives in `lib/api/` and `lib/api/hooks/`. Never hand-edit; regenerate via `just gen-client` then `bunx orval` (or the npm equivalent).

### 4.2 State boundary

Three layers, one rule per layer:

| State category | Library | Persistence | Examples |
|---|---|---|---|
| **Server state** | TanStack Query | None (in-memory cache, optionally `persistQueryClient` for offline) | conversations, messages, friends, presence |
| **Client state (transient)** | Zustand | None | selected conversation, draft text, current call state |
| **Client state (persisted, non-sensitive)** | Zustand + AsyncStorage | AsyncStorage | theme name, biometric-toggle on/off, last-seen timestamps |
| **Client state (persisted, sensitive)** | direct `expo-secure-store` calls | OS keychain/keystore | (v2) refresh tokens, cached credentials |

Rule: never duplicate server state in Zustand. If TanStack Query has it, read it from TanStack Query. Zustand is for what only the client knows.

### 4.3 API client (Orval-generated hooks)

Orval reads `lib/api/schema.ts` (output of `openapi-typescript`) and generates one React Query hook per endpoint, grouped by tag, into `lib/api/hooks/<tag>.ts`. Naming convention: `useGetConversations`, `useSendMessage`, `useUpdateUser`, etc.

Every generated hook routes through the shared `lib/api/client.ts` fetcher, which:

1. Prepends the API base URL from `lib/env.ts`.
2. Sets `credentials: 'include'` so scs cookies round-trip.
3. For mutations, injects `Idempotency-Key: <client-generated UUID v7>` per request (helper in `lib/api/idempotency.ts`).
4. Catches non-2xx responses, parses the apierror envelope (§4.4 of the backend spec), and:
   - Throws a typed `APIError` so React Query's `onError` sees it.
   - Fires a toast via `lib/toast.ts` for any error other than `UNAUTHORIZED` (which we handle separately in §4.7).
5. On 401, clears the local session state and redirects to `(auth)/login.tsx`.

**Mutation toast policy** (matches the operator's "only toasts for some things" rule):

- **Errors** → always show a toast (`burnt.alert({ preset: 'error', title: ... })`).
- **Success** → toast only when `mutation.meta?.toast === true`. The handful of mutations that opt in: `Create*`, `Update*`, `Delete*` for conversations/friends/devices. Sending a message does NOT toast on success.

### 4.4 WebSocket lifecycle

Single persistent connection to `${API_BASE}/v1/ws`. Lives in `lib/ws/client.ts` as a singleton.

**Lifecycle hooks (`lib/ws/lifecycle.ts`):**
- App becomes active (`AppState === 'active'`) AND user is authenticated → connect.
- App backgrounds for >30s → disconnect (battery; the next foreground re-connects).
- Logout → disconnect immediately.
- Connection drops → exponential backoff reconnect (1s, 2s, 4s, 8s, …, capped at 30s). Reset on each successful connect.
- On every successful (re)connect → re-fetch the visible conversation's messages so we close any gap.

**Event dispatch (`lib/ws/dispatcher.ts`):** every server event (`message.new`, `friend.request_accepted`, `room.started`, …) maps to one of three React Query actions:

1. **Invalidate** — list re-fetches on next render. Used for `friend.*`, `presence.update`, `conversation.member_added`.
2. **`setQueryData` mutation** — patch a cached list directly. Used for `message.new` (prepend to the conv's message page) and `message.edited`.
3. **Side-effect** — non-cache action. Used for `room.started` (show the in-app `RoomBanner` + push the call store into `dialing` state if the local user is the initiator), `typing.start` / `typing.stop` (Zustand store).

The dispatcher never owns business state — every server fact still lives in TanStack Query. The dispatcher just translates WS events into Query Cache mutations.

### 4.5 Theming (10 sleep-cycle schemes)

Schemes are pure-frontend — none of these names appear in the backend. The backend's `users.color_scheme` column stays as `light | dark | system` and is treated as the OS-mode hint; the actual themed scheme is stored client-side in AsyncStorage under key `theme:scheme`.

Ten schemes, six light + four dark, named after stages of the day-and-sleep arc:

| Scheme | Mode | Lucide icon | Anchor palette |
|---|---|---|---|
| `sunrise` | light | `Sunrise` | peach `#FFD7B5` on cream `#FFF8EE`, accent `#FF8C5A` |
| `daylight` | light | `Sun` | white `#FFFFFF` on `#FAFAFA`, accent `#1E40AF` |
| `noon` | light | `SunDim` | bleached `#FFFCF0` on `#FFFFFF`, accent `#FBBF24` |
| `golden` | light | `Sunset` | honey `#F4C430` on cream `#FFFBEA`, accent `#B45309` |
| `meadow` | light | `Flower` | sage `#86EFAC` on `#F0FDF4`, accent `#15803D` |
| `dusk` | dark | `CloudSun` | amber `#F59E0B` on slate `#1E293B`, accent `#D97706` |
| `twilight` | dark | `MoonStar` | indigo `#818CF8` on charcoal `#0F172A`, accent `#4F46E5` |
| `aurora` | dark | `Sparkles` | teal `#5EEAD4` on deep blue `#082F49`, accent `#22D3EE` |
| `midnight` | dark | `Moon` | navy `#1E3A8A` on near-black `#020617`, accent `#3B82F6` |
| `rem` | dark | `BrainCircuit` | violet `#A855F7` on plum `#1E1B4B`, accent `#EC4899` |

Plus a `system` pseudo-scheme that reads `Appearance.getColorScheme()` and picks `daylight` (light) or `midnight` (dark).

**Implementation** (`lib/theme/schemes.ts`):
- One token table per scheme: `{ background, foreground, muted, border, accent, accent-foreground, destructive, ring, success }`. Tailwind v5's `@theme` block in `global.css` declares the variables; per-scheme overrides via `[data-theme="midnight"] @theme { … }` or NativeWind's equivalent.
- The picker at `app/settings/theme.tsx` renders 11 swatches (10 + system), each a 96×96 card with the lucide icon + scheme name. Tap → `useThemeStore.setScheme(name)`.
- Scheme persists across launches via AsyncStorage. On first launch, default = `system`.

### 4.6 Toast conventions

Single helper at `lib/toast.ts`, wrapping `burnt`. Three preset shapes:

```typescript
toast.error(title: string, message?: string)
  → burnt.alert({ preset: 'error', title, message, duration: 4 })

toast.success(title: string, message?: string)
  → burnt.alert({ preset: 'done', title, message, duration: 2 })

toast.info(title: string, message?: string)
  → burnt.alert({ preset: 'none', title, message, duration: 2 })
```

Rule of when to fire:

- **Always toast errors** from API mutations (handled inside `lib/api/client.ts`).
- **Toast success** only on these mutations: `Register`, `Login` (only on success after a previous failure), `CreateConversation`, `UpdateConversation`, `AddMembers`, `SendFriendRequest`, `AcceptFriendRequest`, `Block`, `Unblock`, `RegisterDevice`, `UpdateNotificationPrefs`, `UploadAvatar`. Anything else (especially `SendMessage`, `MarkRead`) does NOT toast.
- **Toast info** for ambient events: "Lost connection, reconnecting…" (WS), "Updated to v1.2.3" (post-EAS-Update reload).

### 4.7 Auth + idempotency

- **Cookies, not Bearer.** The backend uses scs cookies (§8.2 of WAKEUP). RN's fetch handles cookies natively on iOS (`URLSession`) and Android (`OkHttp`); `credentials: 'include'` is enough — no extra cookie jar.
- **Auth state is derived from `useGetMe()`.** No separate "is logged in" flag. Query returns 401 → user is logged out; 200 → logged in. The login screen renders if `useGetMe()` returns 401 OR the local session marker is missing.
- **Idempotency keys.** Every POST/PATCH/PUT mutation generates a UUID v7 client-side and sends it in `Idempotency-Key`. Persisted across retries: a `useMutation` retry uses the SAME key (so the backend de-dupes). Helper `lib/api/idempotency.ts` exports `useIdempotencyKey()` which returns a stable key for the lifetime of a single mutation invocation.

### 4.8 Optimistic updates

Pattern for `SendMessage` (the most-used optimistic path):

1. `onMutate(args)`:
   - Generate a client-side message id (UUID v7).
   - Build a placeholder `Message` with `pending: true` flag.
   - Cancel in-flight fetches for the conversation's message page.
   - `setQueryData` to prepend the placeholder.
2. `onSuccess(serverMsg)`:
   - Replace the placeholder by client-id with the real server message (which has the same id, since v7 is deterministic-from-time).
3. `onError`:
   - Mark the placeholder as `failed: true` so the bubble renders an "Tap to retry" UI.
4. WS `message.new` arriving after the optimistic insert finds the placeholder by id and is a no-op.

Same pattern (without WS reconciliation) applies to `SendFriendRequest`, `MarkRead`, and `UpdatePresenceStatus`.

### 4.9 Conventions (locked)

- **One screen per file.** Filename = the segment. Default export = the screen component.
- **Never** call `fetch` directly. Use a generated hook or extend `lib/api/client.ts`.
- **Never** call `useState` for server data. Use TanStack Query.
- **Never** put colour hex values in component code. Use the NativeWind class names tied to the theme tokens.
- **All loading states use the same `<Skeleton>`** (from RNR). Don't roll one-off spinners.
- **All empty states use `<EmptyState icon=… title=… cta=…>`** (custom, in `components/ui/`). Don't compose ad-hoc Text+Button pairs.
- **Errors that already toasted don't also render in-screen.** Pick one surface per error.
- **All images use `<Image>` from `expo-image`**, never RN's stock `<Image>`. Avatars get a blurhash placeholder; message attachments get a low-res placeholder while the full asset loads. Hardcoded RN `<Image>` imports fail lint.
- **All long lists use `<FlashList>`** from `@shopify/flash-list`, never `<FlatList>`. The conversation list and the in-thread `<MessageList>` are the two production hot paths; both must use FlashList with `estimatedItemSize` set realistically.

### 4.10 Resilience & observability

The mobile app is an unreliable client of an unreliable network. Five pieces of plumbing make degraded states safe and debuggable.

#### Root error boundary

`app/_layout.tsx` wraps the route stack in a `<RootErrorBoundary>` (`components/ui/RootErrorBoundary.tsx`). Catches uncaught render-side errors:

1. Logs to Sentry with `Sentry.captureException(error, { tags: { surface: 'react-error-boundary' } })`.
2. Renders a fallback "Something went wrong — try restarting the app" screen with a single "Reload" button (`Updates.reloadAsync()`).
3. Does NOT swallow `SuspenseException` — those bubble to the nearest `<Suspense>` boundary as expected.

The boundary is the absolute last line of defense. Specific screens may add their own narrower boundaries around expensive trees (the conversation thread, the call overlay) so an isolated render failure doesn't take down the whole app.

#### Sentry initialisation

`lib/sentry.ts` initialises Sentry at module import in `app/_layout.tsx`. Config:

- DSN sourced from `process.env.EXPO_PUBLIC_SENTRY_DSN` per build profile.
- `environment` matches the `eas.json` profile (`development`, `preview`, `production`).
- `release` set from `Constants.expoConfig.version + '+' + Updates.runtimeVersion`.
- `tracesSampleRate: 0.1` in production, `1.0` in development.
- An Expo Router integration adds `routeName` as a breadcrumb on every navigation.
- Personally-identifiable data scrubbed: `beforeSend` strips request bodies and any property named `email`, `password`, or `token`.

#### Network state surface

`lib/network/state.ts` wraps `@react-native-community/netinfo` into a singleton + `useNetworkState()` hook returning `{ online: boolean, type: 'wifi' | 'cellular' | 'unknown' }`. Behaviour:

- A persistent thin banner appears at the top of every screen when `online === false`: "You're offline — messages will send when you're back."
- TanStack Query is configured with `networkMode: 'offlineFirst'` globally. Mutations dispatched while offline are queued in TanStack Query's mutation cache (the `MutationCache` is persisted via `persistQueryClient` to AsyncStorage so they survive an app restart) and replayed on reconnect.
- WS reconnect logic listens to NetInfo transitions: switching from wifi to cellular triggers a forced reconnect rather than waiting for the heartbeat to fail.

#### Force-upgrade gate

The backend's `/v1/healthz` returns a `min_client_version` field (shipped in PR #104). On every authenticated foreground:

1. The app polls `GET /v1/healthz` (lightweight, no auth needed).
2. If `min_client_version > Constants.expoConfig.version`, render a full-screen blocking modal: "An update is required. Please update from the App Store / Play Store." with a single deep-link button to the store listing.
3. Modal cannot be dismissed. `Updates.fetchUpdateAsync()` is called as a courtesy in case an EAS Update can satisfy the version bump without a store update.

This protects against the case where a backwards-incompatible API change ships before every client has updated.

#### Offline-aware mutation retry

Every mutation routed through `lib/api/client.ts` is configured with:

```ts
useMutation({
  retry: (failureCount, error) => {
    if (error instanceof APIError && error.status >= 400 && error.status < 500) return false;
    return failureCount < 3;
  },
  retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 30000),
  networkMode: 'offlineFirst',
})
```

4xx errors don't retry (they won't get better). 5xx and network errors retry with exponential backoff. The same idempotency key (§4.7) is used across retries so the backend de-dupes.

### 4.11 Haptics

Single helper at `lib/haptics.ts` wrapping `expo-haptics`. Three preset shapes used app-wide; never call `Haptics.*` directly from components.

```typescript
haptics.tap()      → Haptics.impactAsync(ImpactFeedbackStyle.Light)
haptics.success()  → Haptics.notificationAsync(NotificationFeedbackType.Success)
haptics.warning()  → Haptics.notificationAsync(NotificationFeedbackType.Warning)
```

When to fire (locked):

- **`tap`** — tapping send on the composer, pulling to refresh past threshold, long-press on a message to open the context menu, opening / closing the PiP bubble.
- **`success`** — friend request accepted, conversation created, theme switched, biometric unlock succeeds.
- **`warning`** — incoming call declined, message send fails (the optimistic placeholder flips to `failed: true`).

Do NOT haptic on every server-state change (presence updates, typing indicators, message arrivals). Haptics are for *user-initiated* feedback; ambient chatter does not trigger.

iOS-only by default; on Android the `expo-haptics` calls are no-ops on devices without a haptic engine, which is fine.

### 4.13 In-app event banner

Discord / Slack / iMessage all surface a small slide-down banner at
the top of the screen when a notable event lands while the app is
foregrounded but the user isn't on the relevant screen. Wakeup
mirrors that pattern. The backend doesn't need any new work — every
event below is already published on the WS protocol (`WAKEUP.md` §7.2);
the banner just consumes those events client-side.

**Component: `<EventBanner>`** — mounted at the root layout above
everything except the call overlay. Single instance; queues events and
surfaces them one at a time.

**Events the banner shows:**

| WS event | Banner copy | CTA / route |
|---|---|---|
| `message.new` (from a conv ≠ current route) | "{sender display_name}: {body[0..80]}" | tap → `conversation/[id]` |
| `friend.request_received` | "{sender} sent you a friend request" | tap → `(tabs)/friends` |
| `friend.request_accepted` | "{accepter} accepted your friend request" | tap → `conversation/<auto-DM>` |
| `conversation.member_added` (caller is the newly added member) | "Added you to {group name}" | tap → `conversation/[id]` |
| `room.started` (caller wasn't the initiator) | *(suppressed — `<CallOverlay>` §5.2 handles incoming-call UI)* | *(no banner)* |

**Suppression rules — the dispatcher (`lib/ws/dispatcher.ts`) decides BEFORE calling `bannerStore.enqueue(...)` whether to enqueue. The banner component never makes filtering decisions; it just renders the head of the queue. Don't enqueue when:**
- The user is currently on the conversation screen the message belongs to (`useFocusEffect`-tracked route compared to `event.conversation_id`). They already see the message arrive in the thread.
- The conversation is muted (`muted_until > now()` from §4.12 below) — pushes are gated for muted conversations, banners should be too.
- The user's presence intent is `dnd` — same gate as pushes.
- The event is `room.started` — `<CallOverlay>` already takes the screen.
- A toast for the same event would also fire. `friend.request_received` previously toasted via §6.2's dispatcher; the banner subsumes it, so dropping the toast for that event is part of the Phase 7.5 milestone (§16) — not a separate cleanup PR.

**UX details:**
- 4-second auto-dismiss; tap-to-route also dismisses.
- Swipe-up to dismiss before the timer elapses.
- One at a time. If three events arrive in quick succession, queue them and slide each in after the previous dismisses (200ms gap).
- `haptics.tap()` (light) on appearance per §4.11.
- Theme-aware via NativeWind tokens so colors track the active sleep-cycle scheme.

**Implementation skeleton (`lib/banner/`):**
- `lib/banner/store.ts` — Zustand queue: `enqueue(event)`, `dismissCurrent()`, derived selector for `currentEvent`.
- `lib/banner/EventBanner.tsx` — root-mounted, reads `currentEvent`, animates with `react-native-reanimated`.
- `lib/ws/dispatcher.ts` calls `bannerStore.enqueue(...)` for each banner-eligible event AFTER running the existing `setQueryData` / `invalidateQueries` action. Banner is a non-replacing side-effect on top of the cache update.

**No backend work required.** Schema, fanout, and WS dispatch already
exist for every event in the table above. The banner is pure mobile.

### 4.12 Presence override + per-conversation prefs

Three small client patterns sit on top of the backend's mute / pin / DND additions in `WAKEUP.md` §6.2 and §10.2.

**Sticky DND (`presence_states.intent`).** The `<PresencePicker>` writes through `POST /v1/presence/status`. The local `useGetMe()` cache patches optimistically. The reset option sends `{ status: null }` so the WS hub takes back over. DND should *visually* render the same as away (yellow dot) but with the dot replaced by a red minus-circle — operator confirms this in the per-screen review.

**Mute conversation.** The conversation list reads `muted_until` from each row and renders a small bell-with-slash icon next to the timestamp. Push gating happens server-side; the client only renders the badge. Optimistic update: writing the mute patches `muted_until` in the cached conversation row immediately, then reconciles on success. Forever = `'2099-01-01'`; the UI just renders "Muted" without showing the timestamp when `muted_until > 1 year from now`.

**Pin conversation.** The list's TanStack Query selector sorts pinned-first locally before render. Pinned rows get a thin pin icon at the top-right corner. Optimistic update on toggle: bump or clear `pinned_at` in the cached row and re-sort.

All three of the above are per-member (not per-conversation) — muting a group only mutes it for *me*; the other members keep getting pushes. Same for pin.

**Do not** persist any of these client-side in AsyncStorage. They live in the server response; AsyncStorage is for theme / biometric / draft text only.

---

## 5. Screens & components

### 5.1 Screen inventory

Every screen has: route path, primary endpoints it consumes, primary WS events it reacts to, and a Maestro flow.

| Route | Purpose | Endpoints | WS events |
|---|---|---|---|
| `(onboarding)/index` | 3-screen carousel (welcome → friends value-prop → notifications permission) shown on first launch only. Stores `onboarding:complete = true` in AsyncStorage. | — | — |
| `(auth)/login` | username/email + password | `POST /v1/auth/login` | — |
| `(auth)/register` | new account | `POST /v1/auth/register` | — |
| `(auth)/forgot` | email entry | `POST /v1/auth/password-reset/request` | — |
| `(auth)/reset` | token-confirm form (deep-linked from email) | `POST /v1/auth/password-reset/confirm` | — |
| `(tabs)/index` (Conversations) | list of conversations, sorted by last_message_at, pinned-first. Pull-to-refresh wired to `useGetConversations.refetch()`. Members are inlined on each row in the `GET /v1/conversations` response — no separate members fetch. | `GET /v1/conversations` | `message.new`, `conversation.created`, `conversation.updated`, `conversation.member_added`, `conversation.member_removed`, `room.started`, `presence.update` |
| `(tabs)/friends` | accepted friends + incoming/outgoing requests. Pull-to-refresh refetches all three queries | `GET /v1/friends`, `GET /v1/friends/requests`, `GET /v1/presence/friends` | `friend.*`, `presence.update` |
| `(tabs)/profile` | "me" card + entry to settings | `GET /v1/auth/me` | `presence.update` (self) |
| `search` | global search modal: users + conversations + messages, debounced 200ms. Triggered by a header search icon on the conversations tab. | `GET /v1/search?q=…&types=users,conversations,messages` (shipped in PR #107; `types` is optional, omit for all three) | — |
| `conversation/[id]` | message thread + RoomBanner. Long-press a bubble opens a context menu (copy / react / report / delete-mine). Read-receipt rendering reads `message.read` to mark a sent bubble as "read by N". | `GET /v1/conversations/{id}`, `GET /v1/conversations/{id}/messages`, `POST /v1/conversations/{id}/messages`, `POST /v1/conversations/{id}/read` | `message.new`, `message.edited`, `message.deleted`, `message.read`, `typing.*`, `room.*`, `conversation.updated`, `conversation.member_added`, `conversation.member_removed` |
| `conversation/new` | create group | `GET /v1/users?q=…`, `POST /v1/conversations` | — |
| `conversation/[id]/info` | group info + member list + admin actions. Tap the conversation header in `conversation/[id]` to open. Group admins see Add Member + Remove Member; every member sees Leave Group. DM rendering of this screen is the peer's profile (no member list, no leave). | `GET /v1/conversations/{id}`, `POST /v1/conversations/{id}/members` (add), `DELETE /v1/conversations/{id}/members/{user_id}` (admin remove), `DELETE /v1/conversations/{id}` (caller leaves) | `conversation.updated`, `conversation.member_added`, `conversation.member_removed` |
| `settings/account` | display name, avatar, password change, logout, delete-account entry | `PATCH /v1/users/me`, `POST /v1/users/me/avatar`, `POST /v1/auth/logout` | — |
| `settings/privacy` | biometric toggle, lock-after picker | local AsyncStorage only | — |
| `settings/notifications` | category toggles | `GET/PATCH /v1/users/me/notifications` | — |
| `settings/theme` | 10-scheme + system swatch picker. Switching the scheme also switches the iOS app icon variant (see §10.5) and reloads the splash. | local AsyncStorage only | — |
| `settings/devices` | list + revoke push tokens | `GET /v1/devices`, `DELETE /v1/devices/{id}` | — |
| `settings/blocked` | list of blocked users + unblock action | `GET /v1/blocks`, `DELETE /v1/blocks/{userId}` | — |
| `settings/profile-edit` | display name, bio (≤280 chars), status emoji picker, avatar | `PATCH /v1/users/me` (bio + status_emoji + display_name), `POST /v1/users/me/avatar` | — |
| `settings/contacts` | one-time contact sync: request OS permission, hash entries client-side, POST hashes, render matched users + "Send invite" button per unmatched | `POST /v1/contacts/match` | — |
| `user/[id]` | view another user's public profile — display name, bio, status emoji, avatar, presence dot, friend / message / block actions | `GET /v1/users/{id}`, `GET /v1/presence/friends` (cached) | `presence.update` |
| `settings/delete-account` | App-Store-mandated account deletion. Confirmation dialog → password re-entry → `DELETE /v1/users/me`. Logs out, clears all local state, redirects to `(auth)/login`. | `DELETE /v1/users/me` | — |
| `settings/about` | version, build number, runtime version, links to Privacy Policy + Terms (web URLs). Tap version 7× to reveal a debug panel (Sentry test crash button, network state, last WS event). | — | — |
| `(tabs)/admin` | **Admin-only.** Landing page with three cards: Users, Audit Log, Active Impersonation. Rendered only when `useGetMe().data.role === 'admin'`. Non-admins never see the tab and can't deep-link in (route guard 403s + redirects to `(tabs)/index`). | `GET /v1/auth/me` | — |
| `admin/users` | Searchable user table — paginated list with role + lock badges. Tap row → `admin/user/[id]`. | `GET /v1/admin/users?q=…&limit=…&cursor=…` | — |
| `admin/user/[id]` | Admin view of a single user (includes soft-deleted). Actions: change role, lock/unlock account, impersonate. | `GET /v1/admin/users/{id}`, `PATCH /v1/admin/users/{id}`, `POST /v1/admin/users/{id}/impersonate` | — |
| `admin/audit` | Paginated audit log viewer — who did what to whom, when. Filter by actor, target, action. | `GET /v1/admin/audit?actor=…&target=…&action=…&cursor=…` | — |

### 5.2 Component inventory (custom, beyond RNR)

- `<EventBanner>` — root-mounted, single instance, queues banner-eligible WS events and slides each one down from the top per §4.13. Suppressed on the conversation screen for that conversation, on muted conversations, and for users in DND.
- `<RoomBanner conversationId>` — top of conversation screen. Reads `useGetRoomState(id)`. Three render states: hidden, "Join call (N in room)", "X is calling…" (Accept / Decline buttons).
- `<CallOverlay>` — global, mounted in root layout. Renders nothing when call store is `idle`. Two modes: full-screen (when current route ≠ a call-bearing conversation) → minimised PiP. The store decides which mode based on `useFocusEffect` from Expo Router.
- `<ParticipantTile userId roomId>` — voice mode = avatar + speaking pulse animated via Reanimated. Video mode = `<VideoTrack>` from LiveKit. Both styles in one card with consistent shadow/border.
- `<ControlBar>` — three icon buttons at the bottom of `<CallOverlay>`: mic (toggle), camera (toggle), leave (red). Optional fourth: camera-flip when video is on.
- `<DraggablePip>` — Reanimated gesture handler, snaps to four corners, double-tap to expand, single-tap is a no-op (avoids accidental dismiss).
- `<MessageList>` — gifted-chat shell with custom `renderBubble`, `renderAvatar`, `renderTime`. Reads from infinite query.
- `<Composer>` — text input + attach + send. Manages typing-indicator throttle (publish `typing.start` once, `typing.stop` after 3s of no input).
- `<FriendRow>` — avatar + name + presence dot + status pill ("3 unread", "in a call").
- `<PresenceDot status>` — animated dot, four colours (online green, away yellow, sleeping blue, offline gray).
- `<EmptyState icon title subtitle cta>` — single empty-state primitive (mentioned in §4.9).
- `<BiometricGate>` — root-level overlay; conditionally rendered when `useBiometricStore` says "locked".
- `<ToastRoot>` — `BurntProvider` from burnt, mounted once at root.
- `<RootErrorBoundary>` — root-level React error boundary per §4.10. Reports to Sentry, shows fallback + Reload button.
- `<NetworkBanner>` — thin offline indicator surfaced at the top of every screen when `useNetworkState().online === false`.
- `<ForceUpgradeGate>` — full-screen blocking modal when `min_client_version > current` per §4.10.
- `<OnboardingCarousel>` — three-screen swipeable intro shown once on first launch. Final screen requests notification permission before handing off to `(auth)/login`.
- `<MessageContextMenu>` — wraps RNR's `<ContextMenu>` around `<ChatBubble>`. Long-press on a bubble surfaces: Copy, React (v2 stub for now), Report, Delete (own messages only).
- `<PullToRefresh>` — thin wrapper around RN's `RefreshControl` that fires `haptics.tap()` past the trigger threshold and surfaces a themed spinner. Used by the conversations list and friends tab.
- `<AppIconSwitcher>` — invoked from the theme picker. Calls `expo-dynamic-app-icon` (or the equivalent native module) with the icon name matching the selected scheme. iOS-only; on Android the launcher icon is fixed (per §10.5).
- `<SplashScreenProvider>` — root-mounted. Reads the persisted scheme from AsyncStorage *before* React mounts (synchronous storage check) and applies the matching splash image. Calls `SplashScreen.hideAsync()` once theme + auth state have hydrated.
- `<HapticTrigger>` — invisible event-tap helper used inside `<Pressable>` wrappers when imperatively firing a haptic from a parent is awkward. Most code uses the `lib/haptics.ts` helpers directly; `<HapticTrigger>` is for declarative cases (gesture handler `onActivate` etc.).
- `<PresencePicker>` — bottom-sheet from the profile tab letting the user set sticky presence. Five options: Online, Away, Sleeping, Do Not Disturb, "Reset (let the app decide)". Tapping calls `POST /v1/presence/status` with `{ status: 'dnd' | … | null }` and updates the local cache. DND shows the user on their friends' lists with a red dot + a small "Do Not Disturb" caption. The same picker is reachable via long-press on the user's own avatar in the conversations list header.
- `<StatusEmojiPicker>` — RNR `<Popover>` over a 6-column grid of common emoji + a "type your own" text field. Selected emoji and any clearable "no emoji" choice round-trip through `PATCH /v1/users/me` with `{ status_emoji: "🛌" | "" | null }`.
- `<MuteSheet>` — RNR `<ActionSheet>` with options: 15 min · 1 hr · 8 hr · 24 hr · Until I turn it back on (= timestamp `2099-01-01`) · Unmute. Wired to `PATCH /v1/conversations/{id}/mute`. Triggered from the conversation header's overflow menu and from a long-press on the conversation row in the list.
- `<PinToggle>` — single button in the same long-press menu / header overflow. Calls `PATCH /v1/conversations/{id}/pin` with `{ pinned: !current }`. Optimistic update on the conversation list resort.
- `<ContactSyncEmptyState>` — the screen body for `settings/contacts`. Three states: not-yet-synced (CTA "Find friends from your contacts"), syncing (spinner + count), synced (matched-users list + invite buttons for unmatched).
- `<AdminTabGuard>` — wraps the admin tab's `_layout` and the `admin/*` route group. Reads `useGetMe().data.role`, redirects non-admins to `(tabs)/index`, and renders nothing while `useGetMe()` is loading (avoids a tab-flicker on cold start).
- `<ImpersonationBanner>` — global, mounted in root layout above the tab bar. Reads `useGetMe().data.impersonated_by` (a `{ id, username, display_name }` object the backend returns on `GET /v1/auth/me` while an admin session has `impersonating_user_id` set per WAKEUP §8.7). When non-null: a high-contrast warning banner across the top of every screen — "Impersonating <username> — End session" — tapping End calls `POST /v1/admin/impersonate/end` and invalidates the `me` query so the banner falls away.

### 5.3 Admin tab (admin users only)

The Admin tab is the mobile UI on top of the backend's existing `/v1/admin/*` routes (already implemented — `admin_handler.go` exposes list users, get user, patch user, impersonate start/end, and audit log).

**Visibility rule:** `useGetMe().data.role === 'admin'` is the single source of truth. The tab bar reads it; route guards re-check it on every navigation.

**Three sub-screens:**

1. **Users** (`admin/users.tsx`) — debounced search + paginated `<FlashList>`. Each row shows display name, username, role badge (`user` / `admin`), and a lock icon if `locked_at` is set. Tap → user detail.
2. **User detail** (`admin/user/[id].tsx`) — read-only profile + a `<Card>` of admin actions:
   - Change role (`user` ↔ `admin`) → `PATCH /v1/admin/users/{id}` with `{ role }`.
   - Lock / unlock account → same PATCH with `{ locked: true/false }`.
   - Impersonate → `POST /v1/admin/users/{id}/impersonate`. On success, the global `<ImpersonationBanner>` activates and the app reloads `useGetMe()`.
3. **Audit log** (`admin/audit.tsx`) — paginated table of admin actions from `GET /v1/admin/audit`. Each row is `actor → action → target — timestamp`. Filterable by actor, target, action. No write operations.

**Confirmation discipline:** every destructive admin action (role change, lock, impersonate, end-impersonation) requires a confirmation dialog (RNR's `<AlertDialog>`). No long-press shortcuts.

**Haptics:** admin actions fire `haptics.warning()` (not `success`) — admin work is intentionally heavyweight, the haptic should feel deliberate.

**Maestro flows:** `admin-list.yaml`, `admin-user-detail.yaml`, `admin-impersonate-start-end.yaml`, `admin-audit.yaml`. The flows depend on a seeded admin account in the test fixtures (track adding to backend test seeds as a follow-up).

---

## 6. Real-time integration

### 6.1 Subscription model

The WebSocket connection auto-subscribes to channels for every conversation the user is a member of. The backend's WS bridge fans events to the right user; the client just consumes them.

### 6.2 Event → Query Cache table

| WS event | Action |
|---|---|
| `message.new` | `setQueryData` prepend to `['messages', convId]`; bump conversation row's `last_message_at` in `['conversations']`; if not on the conversation screen, increment unread badge |
| `message.edited` | `setQueryData` patch in `['messages', convId]` |
| `message.deleted` | `setQueryData` mark deleted in `['messages', convId]` |
| `friend.request_received` | `invalidateQueries(['friends', 'requests'])`; enqueue an `<EventBanner>` event (per §4.13). The earlier draft toasted here — the banner subsumes that surface so don't double-fire. |
| `friend.request_accepted` | `invalidateQueries(['friends'])`; enqueue an `<EventBanner>` event (per §4.13). |
| `presence.update` | `setQueryData` patch in `['presence', 'friends']` |
| `room.started` | `invalidateQueries(['room', convId])`; if not the initiator, show in-app `RoomBanner` "X is calling…" with Accept/Decline |
| `room.participant_joined` | `setQueryData` patch in `['room', convId]`; if local user is initiator and count == 2, transition call store from `dialing` → `active` |
| `room.participant_left` | `setQueryData` patch; if count == 1 and local user is the survivor, render "Waiting for others…" UI; backend's lone-kick will fire RemoveParticipant after 5min |
| `room.video_changed` | `setQueryData` patch (one participant's video flag) |
| `room.ended` | clear `['room', convId]`; if call store was active for this conv, reset to `idle` |
| `typing.start` / `typing.stop` | Zustand store `useTypingStore` (not Query Cache) |

### 6.3 Connection state surface

A small `useWSConnectionState()` hook exposes `'connected' | 'reconnecting' | 'disconnected'`. The conversation screen renders a thin "Reconnecting…" banner when state ≠ connected for more than 2s, and a one-time "Reconnected" toast when it recovers.

---

## 7. Push notifications

### 7.1 Registration

On first authenticated launch:

1. Call `Notifications.requestPermissionsAsync()`. Skip the rest if denied.
2. Get the Expo push token via `Notifications.getExpoPushTokenAsync({ projectId })`.
3. POST `/v1/devices` with the token + platform.
4. Persist `device:registered = true` in AsyncStorage so we don't re-register every launch.

If the token rotates (Expo can rotate), the listener at `Notifications.addPushTokenListener` POSTs the new token.

### 7.2 Categories

Backend categories (from `WAKEUP.md` §11.5): `direct_messages`, `group_messages`, `friend_requests`, `calls`. Settings screen exposes one switch per category, calling `PATCH /v1/users/me/notifications`.

### 7.3 Notification handlers

- **Foreground** (`Notifications.setNotificationHandler`): suppress system banner — the in-app surfaces (`<EventBanner>` per §4.13, RoomBanner, unread dot, friend request list) already show it. Exception: incoming call → still play the sound but no banner.
- **Background tap** (`Notifications.addNotificationResponseReceivedListener`): route based on `notification.data.type`:
  - `message` → `conversation/[id]`
  - `friend_request` → `(tabs)/friends`
  - `call` → `conversation/[id]` then auto-join the room (`Join` mutation with `video: false`)

### 7.4 Notification action buttons

iOS supports action buttons (Accept / Decline) in the notification itself via `NotificationCategory`. Wire up exactly one category — `INCOMING_CALL` — with two buttons:

- Accept (foreground action) → opens the app to the conversation, auto-joins.
- Decline (background action) → no-op locally; the call continues without us.

Android shows the same two buttons via `expo-notifications` action support.

### 7.5 Notification grouping + badge count

- **Thread-id grouping.** Every push payload includes `data.thread_id = conv:<conversationId>` (DMs, groups) or `friend_requests` (friend tab). iOS uses `apns-collapse-id` + `threadIdentifier`; Android uses `groupKey`. Notifications from the same conversation collapse into one stack on the lock screen instead of producing N separate banners.
- **Badge count.** The backend returns the user's total unread count in every WS heartbeat (`unread_total`). The client mirrors that into the app icon badge via `Notifications.setBadgeCountAsync(n)`:
  - On WS heartbeat: set badge to `unread_total`.
  - On `MarkRead` mutation success: optimistic decrement.
  - On full launch: `useGetMe()` returns the same count via a header (`X-Unread-Total`) so the badge is correct before WS connects.
- **Clear on background → foreground.** When the app foregrounds and lands on the conversation that received the latest push, dismiss the matching notification group: `Notifications.dismissNotificationsByThreadIdentifierAsync('conv:<id>')` on iOS, `cancel(notificationId)` on Android.

---

## 8. Voice & video

### 8.1 Connection sequence

1. User taps "Join call" or accepts an incoming-call notification.
2. Client checks mic permission via `expo-audio`'s `Audio.requestPermissionsAsync`. If denied, surface a toast and stop. (Camera permission is requested only when the user toggles video on.)
3. `useJoinRoom(conversationId)` mutation hits `POST /v1/conversations/{id}/room/join` with `{ video: false }`. Backend returns `{ room_id, livekit_url, livekit_token, expires_at }`.
4. Pass to LiveKit: `room.connect(livekit_url, livekit_token)`.
5. Set `react-native-incall-manager.start({ media: 'audio' })` so the device routes audio to the speaker rather than the earpiece.
6. Call store transitions to `dialing` (if no other participants yet) or `active` (if others are already in).

### 8.2 Mid-call state

- Mic: `room.localParticipant.setMicrophoneEnabled(boolean)`.
- Camera: `room.localParticipant.setCameraEnabled(boolean)`. Triggers `track_published`/`track_unpublished` on the backend → `room.video_changed` WS event back to other clients.
- Speaking indicator: subscribe to each participant's `audioLevel` track event; pulse animation when level > threshold.

### 8.3 Picture-in-picture (in-app only)

When the user navigates away from the call-bearing conversation:
1. `useFocusEffect` in `<CallOverlay>` detects focus loss.
2. Call store transitions `active` → `pip`.
3. Render mode switches to a 96×128 floating bubble showing the "active speaker" tile (the participant with the highest current audioLevel).
4. `<DraggablePip>` snaps to corners. Tap to expand back to full screen (route goes back to the conversation).

True OS-level PiP (the bubble survives app backgrounding) is v2.

### 8.4 Leaving the call

- Tap Leave → `room.disconnect()` → `react-native-incall-manager.stop()` → POST `/v1/conversations/{id}/room/leave` → call store back to `idle`.
- Backgrounding the app does NOT leave the call — audio keeps flowing via the iOS `voip` background mode (declared in `app.json` per §13).

### 8.5 Edge cases

- **Token expiry mid-call**: LiveKit's SDK refreshes automatically. If refresh fails (e.g., session cookie expired), surface a toast and disconnect.
- **Lone-user kick**: server-side after 5min alone (backend §10.3). Client experiences this as a normal `room_finished` → call store resets to `idle`. Render a toast: "Call ended — everyone else left."
- **WS disconnect during call**: LiveKit has its own connection; the app's WS is separate. Losing the app WS doesn't drop the call.

### 8.6 Native call UI (CallKit + ConnectionService)

VoIP apps that don't integrate with the OS call UI fail App Store / Play Store review. We use `react-native-callkeep` as a single API over both.

#### iOS — CallKit + PushKit

1. **VoIP push token.** On first authenticated launch (after the regular Expo push token), call `RNCallKeep.setup(...)` then register a PushKit token via `react-native-voip-push-notification`. POST it to the backend at `/v1/devices/voip` (shipped in PR #105 — different token type from the existing `/v1/devices` Expo path).
2. **Incoming call wake.** When the backend has an incoming-call event for a fully-killed-app user, it sends a PushKit payload (silent, high-priority). The OS wakes the app long enough to call `RNCallKeep.displayIncomingCall(...)`. CallKit owns the UI from there (full-screen ring, lock-screen accept/decline).
3. **Accept handler.** `RNCallKeep` events bridge to our app:
   - `answerCall` → join the LiveKit room.
   - `endCall` → leave the LiveKit room + POST `/v1/conversations/{id}/room/leave`.
   - `didActivateAudioSession` → `react-native-incall-manager.start({ media: 'audio' })`.
4. **Audio session integration.** CallKit's audio session takes priority over Siri / Music / podcasts automatically. We just need to NOT start an `AVAudioSession` ourselves while a CallKit session is active.

#### Android — ConnectionService

`react-native-callkeep` uses `ConnectionService` to expose the call to the system in-call UI (so users can switch to Bluetooth, the dialer shows our call, etc.). Same JS API:

1. `RNCallKeep.setup({ android: { alertTitle: '…', alertDescription: '…' } })` on launch.
2. Foreground service permission (`FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_PHONE_CALL`) declared in `app.json` → `android.permissions`.
3. Incoming-call handling uses a high-priority FCM data message (Expo push handles this; no separate token type needed on Android).

#### Why this matters

Without CallKit / ConnectionService:
- iOS users on lock screen see a banner-style notification instead of the full ring UI; can't answer one-handed.
- Apple rejects the app at review for "incomplete VoIP integration."
- Android users can't see the call in the system tray, can't switch audio routing from quick settings.

This is non-optional for v1. Build it in Phase 9.

---

## 9. Local storage

### 9.1 AsyncStorage keys

| Key | Type | Purpose |
|---|---|---|
| `theme:scheme` | string (one of the 10 + `system`) | Selected colour scheme |
| `biometric:enabled` | boolean | Lock toggle |
| `biometric:lock_after` | number (seconds) | Re-lock timeout (default 0 = immediate) |
| `biometric:last_unlock` | number (unix ms) | Last successful unlock |
| `device:registered` | boolean | Whether we've POSTed our push token |
| `device:expo_token` | string | Cached token (compared against listener) |
| `device:voip_token` | string | iOS PushKit token (separate from the Expo token) |
| `composer:draft:<convId>` | string | Persisted draft per conversation |
| `unread:<convId>` | number | Local unread count (cross-checked against `last_read_message_id` on next fetch) |
| `onboarding:complete` | boolean | First-launch carousel was finished or skipped |
| `tracking:prompted` | boolean | App Tracking Transparency prompt shown (don't ask again) |
| `query-cache:v1` | json | Persisted TanStack Query cache (mutations + selected queries) |
| `mutation-cache:v1` | json | Persisted offline-mutation queue (replayed on reconnect) |

### 9.2 SecureStore keys

Reserved for v2 (refresh tokens, cached credentials). v1 stores nothing in SecureStore.

### 9.3 React Query persistence

Optional, off by default. If we enable `persistQueryClient` (likely v1.5 for offline-friendliness), it writes to AsyncStorage under `query-cache:v1`. Stale queries above the configured `maxAge` are dropped on hydration.

---

## 10. Privacy & security

### 10.1 Biometric app lock

- `<BiometricGate>` lives in the root layout, above all routes.
- On app foreground: if `biometric:enabled === true` AND `now - biometric:last_unlock > lock_after_seconds`, render the gate.
- The gate is a full-screen view with the app icon, app name, and a single "Unlock" button.
- Tap → `LocalAuthentication.authenticateAsync({ promptMessage: 'Unlock Wakeup', fallbackLabel: 'Use Passcode' })`.
- On success → write `biometric:last_unlock = Date.now()`, dismiss the gate, show the app.
- On cancel → gate stays. No way to bypass.

### 10.2 Settings UX

```
Profile → Privacy & Security
  ☐  Require Face ID to open       <Switch>     (label adapts: "Touch ID" / "biometrics")
       Lock the app when you switch away. Re-unlock with
       Face ID, Touch ID, or your device passcode.

  Lock after:   [Immediately ▾ | 1 min | 5 min | 1 hour]
       (only enabled when the toggle is on)
```

### 10.3 Session expiry

If `useGetMe()` returns 401 mid-session:
1. Clear all AsyncStorage keys except `theme:*` and `biometric:*` (we want those to persist across logins).
2. Disconnect WS.
3. Disconnect any active LiveKit room.
4. Redirect to `(auth)/login`.

### 10.4 What we do NOT store

- Plain-text passwords. Login form fields are not persisted.
- Session cookie content. The OS cookie jar owns it; we never read or copy it into our storage.
- LiveKit tokens. Issued per-join, expire in 10 min, never written to disk.

### 10.5 App Store / Play Store compliance

A v1 release that fails store review is a non-release. The following are non-negotiable.

#### iOS privacy manifest (Required Reasons API)

Apple requires a `PrivacyInfo.xcprivacy` manifest declaring every "Required Reason" API the app calls. Maintained at `apps/mobile/ios/Wakeup/PrivacyInfo.xcprivacy` (generated by Expo prebuild from a config plugin). Declare:

- `NSPrivacyAccessedAPICategoryUserDefaults` reason `CA92.1` (app functionality — AsyncStorage uses UserDefaults).
- `NSPrivacyAccessedAPICategoryFileTimestamp` reason `C617.1` (display to user — image cache freshness).
- `NSPrivacyAccessedAPICategorySystemBootTime` reason `35F9.1` (measure performance — Sentry).
- `NSPrivacyAccessedAPICategoryDiskSpace` reason `E174.1` (write to disk — image cache management).

Plus a `NSPrivacyCollectedDataTypes` array listing what we collect (display name, email, contact info, audio data during calls, etc.) and the purposes (app functionality, analytics for Sentry).

Re-audit every time we add a new dependency. The Expo SDK provides starting manifests for its own modules; third-party libs (LiveKit, Sentry) need their manifest entries merged in.

#### App Tracking Transparency (ATT)

Even though we don't track across apps, Apple requires the prompt for any app that *could* (i.e., uses any third-party SDK that includes the IDFA). On first authenticated launch (after onboarding, before the conversation list):

1. Check `tracking:prompted` in AsyncStorage. If true, skip.
2. Call `requestTrackingPermissionsAsync()` from `expo-tracking-transparency`.
3. Persist `tracking:prompted = true` regardless of outcome.

Result is informational only — we don't gate anything on the answer because we don't actually track.

#### Account deletion (Apple-required)

Apple requires apps with account creation to provide an in-app account deletion path that's no harder to find than sign-up was. The `settings/delete-account` screen handles this:

1. Big red "Delete account" button at the bottom of `settings/account`.
2. Tap → modal: "This will permanently delete your account, all conversations you started, and all your messages. Friends will see [redacted] in past chats."
3. Re-enter password → `DELETE /v1/users/me` (already implemented server-side; performs the soft-delete + tombstone defined in the backend spec).
4. On success: clear all AsyncStorage, disconnect WS + LiveKit, navigate to `(auth)/login`.

#### Universal links / deep links

Configured in `app.json` `ios.associatedDomains` and `android.intentFilters`. The link host is `wakeup.app`. Routes:

- `https://wakeup.app/c/<conversationId>` → opens `conversation/[id]`.
- `https://wakeup.app/u/<username>` → opens DM with that user (auto-create on first message per §5.3).
- `https://wakeup.app/r/<token>` → password reset (existing).
- `https://wakeup.app/i/<inviteCode>` → friend invite landing (post-v1; reserved for later).

Server-side: the backend serves `apple-app-site-association` and `assetlinks.json` at the well-known paths (shipped in PR #104; both endpoints return 404 when the corresponding `IOS_APP_ID` / `ANDROID_PACKAGE` env keys are unset, so dev environments don't have to opt in).

#### Accessibility baseline

Both iOS and Android stores audit accessibility now. Every screen must:

- Have `accessibilityLabel` on every `<Pressable>`, `<Button>`, and image button (avatars, control bar icons, friend rows).
- Pass `accessibilityRole` correctly (button, link, image, header).
- Honor Dynamic Type / Font Scaling — no hardcoded `fontSize` smaller than 12, all text scales with the OS setting.
- Honor `prefers-reduced-motion`: the call PiP bubble, the speaking pulse, and the onboarding carousel all reduce / disable animation when `useReduceMotion()` returns true.
- Achieve WCAG AA contrast (4.5:1 text on background) in all 10 themes — verify per scheme during the operator-review loop.

A baseline audit pass at the end of Phase 10 (theme + biometric) using `accessibility-inspector` on iOS Simulator and TalkBack on Android Emulator is part of the milestone gate.

#### App icon variants per scheme (iOS 18+)

iOS 18 supports multiple alternate icons keyed off a string. We ship 11 icons:

- `default` (matches the `system` scheme default = `daylight` for light mode, `midnight` for dark).
- One icon per named scheme (`sunrise`, `daylight`, `noon`, `golden`, `meadow`, `dusk`, `twilight`, `aurora`, `midnight`, `rem`).

Each icon uses the scheme's anchor palette + the matching lucide icon as the focal element. Stored under `assets/icons/<scheme>.png` (1024×1024 plus the iOS sizes Expo expects). Selection lives in `<AppIconSwitcher>`; the theme picker fires `setAppIcon(scheme)` on tap.

Android does NOT support alternate launcher icons cleanly (only via activity-aliases, which trigger a flicker and an "app updated" notification). On Android, the launcher icon stays fixed at the `daylight`-anchored default. The theme picker UI explicitly notes this on Android.

#### Splash screen per scheme

The splash screen is the very first thing the user sees on launch. It should match the user's last-selected scheme so the transition into the app is seamless.

- Default (first launch, before any AsyncStorage) → `system`-scheme splash (white-on-light or near-black-on-dark based on `Appearance.getColorScheme()`).
- After first scheme selection → splash for that scheme. Stored under `assets/splash/<scheme>.png`.
- `<SplashScreenProvider>` reads AsyncStorage synchronously *before* React mounts. The Expo-managed splash takes the value via `expo-splash-screen` API or the runtime variant of `app.json`.

#### App metadata (App Store + Play Store)

Stored under `apps/mobile/store/`:

- `apps/mobile/store/ios/` — `description.txt` (4000 char limit), `keywords.txt`, `support_url.txt`, `marketing_url.txt`, `privacy_policy_url.txt`, `screenshots/<device>/<scheme>/*.png` (10 schemes × 5 screen × 3 device sizes = a lot — automate via Maestro `takeScreenshot` per scheme).
- `apps/mobile/store/android/` — `full_description.txt` (4000 chars), `short_description.txt` (80 chars), `feature_graphic.png`, `screenshots/<scheme>/*.png`.
- `apps/mobile/store/CHANGELOG.md` — shipped as `What's New` text per release.
- `apps/mobile/store/icons/` — high-res 1024×1024 icon source files.

Owned by the operator, not the implementer. The implementer's job is to maintain the directory structure and the screenshot automation hooks.

---

## 11. Widgets

### 11.1 Scope (v1)

A single widget per platform: **Friends**. Renders up to 6 friend rows with avatar + name + presence dot. Tapping a row deep-links into the corresponding DM (auto-creates if needed). Refresh schedule: every 15 minutes, or when the app foregrounds.

### 11.2 Data source

The widget process is separate from the app process — it can't reuse the app's TanStack Query cache. Instead:

- **iOS**: SwiftUI WidgetKit timeline. The widget's TimelineProvider calls `GET /v1/widget/friends` directly using `URLSession` with the shared cookie jar (App Group + Keychain). The endpoint returns a compact slim payload (id, display_name, avatar_url, status) of the user's most-active friends.
- **Android**: `react-native-android-widget` worker runs JS code in a constrained environment. Uses the same `/v1/widget/friends` endpoint; the cookie comes from the same OkHttp store the app uses.

### 11.3 Implementation references

- iOS: `@expo/ui/swift-ui` Expo skill — the widget target lives in `widgets/ios/` and ships as part of the EAS Build.
- Android: `react-native-android-widget` config in `app.json`'s plugin array; widget definition in `widgets/android/FriendsWidget.tsx`.

### 11.4 Out of scope for v1

- Conversation widget (would show recent unread). Defer to v2.
- Status widget (lets you change your own status from the home screen). Defer to v2.

---

## 12. Testing strategy

### 12.1 Hard rules

- Every screen in §5.1 has a Maestro flow at `.maestro/<route-name>.yaml`.
- Every screen has the operator review the rendered UI **on a real phone via the Expo Go QR code** before the milestone is checked off (see §12.5 below). Maestro MCP screenshots are the implementer-facing sanity check; the operator's phone scan is the gate.
- Every Zustand store has a unit test covering its reducer logic.
- Every component in `components/` with non-trivial state (composer's draft + typing indicator, draggable PiP, biometric gate) has a render test.
- API hooks themselves (Orval-generated) are NOT unit-tested — that's testing the generator. Test the integration via Maestro flows.

### 12.2 Maestro discipline

Each `.maestro/<flow>.yaml` file follows the same shape:

```yaml
appId: app.wakeup.client
---
- launchApp:
    clearState: true
- runFlow: ./flows/login.yaml      # shared sub-flow
- tapOn: "Conversations"
- assertVisible: "No conversations yet"
# … per-screen assertions …
- takeScreenshot: <screen-name>
```

`flows/` holds shared sub-flows: `login.yaml`, `register.yaml`, `seed-friend.yaml`. The Maestro MCP runs these and surfaces screenshots back to the operator for the per-milestone review loop.

### 12.3 What we DON'T test

- LiveKit connection / SFU behaviour. Tested in the backend's §12.8.4 e2e suite.
- Backend invariants. Tested in the backend's `internal/handler/http/*_test.go`.
- Native widget rendering. Manual verification (Maestro doesn't drive home-screen widgets).

### 12.4 The operator-review loop

Per the rule in §0, every milestone in §15 ends with: implement → write Maestro flow → run flow via the Maestro MCP → boot the dev server with `bunx expo start --tunnel` → operator scans the QR with Expo Go on their phone → operator reviews → approve or correct → commit.

The operator's correction is the final word. Don't loop for purely cosmetic preferences without explicit instruction.

### 12.5 Expo Go QR + tunnel mode (the per-screen review gate)

`bunx expo start --tunnel` is the canonical command for the review loop. `--tunnel` matters: it routes the Metro bundle through ngrok-style tunneling so the operator's phone can connect over any network (cell data, separate Wi-Fi, etc.) without LAN constraints. Side benefits the operator gets for free:

- **Phone preview** — scan the QR with iOS Camera (deep-links Expo Go) or Android's Expo Go app. The build hot-reloads as code changes.
- **Web preview on the laptop** — same `expo start` shows a `Web` shortcut. Useful for quick visual checks without picking up the phone.
- **Multiple devices simultaneously** — the operator can have iPhone, an Android tablet, and the laptop browser all connected to the same tunnel. Useful for visually verifying real-time features (open the same conversation on two devices, send a message from one, watch it appear on the other).

**The implementer's loop per screen:**
1. Build the screen.
2. Write the Maestro flow.
3. `bunx expo start --tunnel` from the repo. Confirm Metro is up.
4. Run the Maestro flow via the Maestro MCP. Capture screenshots into the per-milestone PR description.
5. **Stop and ping the operator.** Ask them to scan the QR code. Wait for explicit approval before committing — even if Maestro screenshots look fine.
6. Apply corrections from the operator's review.
7. Commit + push + open PR.

The "stop and ping" is non-negotiable. The point of this build cadence is the operator-on-phone review, not "the screenshots looked OK."

### 12.6 Maestro MCP setup

The Maestro MCP (`@mobile-dev-inc/maestro-mcp`) is installed at the start of Phase 0 alongside the rest of the toolchain (§14.1). It exposes tools to:

- Start a Maestro test against a running simulator.
- Read flow output and screenshots back into the implementer's transcript.
- Query the device hierarchy (so the implementer can debug "why didn't this tap work").

The MCP only drives the iOS Simulator / Android Emulator — it does NOT drive Expo Go on a physical device. That's the operator's job via the QR code.

### 12.7 Flow catalog (v1)

Every entry below is a YAML file under `.maestro/flows/` (or one level deeper for shared sub-flows). The implementer creates the file the first time the matching milestone in §16 is touched. CI fails if a screen in §5.1 has no flow file.

**Shared sub-flows** (used by every screen-level flow via `runFlow:`):

| File | What it does |
|---|---|
| `flows/_shared/login.yaml` | `clearState: true` + tap email field + type seeded test user creds + tap Submit + assert `(tabs)/index` is visible. Every authenticated flow starts with this. |
| `flows/_shared/register.yaml` | New-account flow. Used by `auth-register.yaml` directly and seeded into per-test-suite cleanup. |
| `flows/_shared/seed-friend.yaml` | Logs in, navigates to Friends, sends a request to a second seeded user. Required by every flow that needs an existing friendship (DM auto-create, conversation create, presence assertions). |
| `flows/_shared/seed-conversation.yaml` | Logs in and creates a direct conversation with the seeded peer. Required by `conversation-thread.yaml` and onward. |

**Per-screen flows** (one per `.tsx` route in §5.1):

| File | Asserts |
|---|---|
| `auth-login.yaml` | login form submits → tab bar visible. |
| `auth-register.yaml` | register form submits → tab bar visible. |
| `auth-forgot-password.yaml` | enter email → "Check your email" copy visible. |
| `auth-reset-password.yaml` | deep-link from URL with token + form submits → login screen visible. |
| `conversations-empty.yaml` | fresh user → "No conversations yet" copy visible. |
| `conversations-list.yaml` | seed two conversations → both rows render in `last_message_at` order. |
| `conv-pin-mute.yaml` | long-press a row → pin toggles (row floats to top); mute opens sheet → 1hr selection updates row badge. |
| `conversation-create.yaml` | "+" button → multi-select two friends + group name → `(tabs)/index` shows new row. |
| `conversation-thread.yaml` | open existing conversation → message list renders with two seeded messages, newest at the bottom. |
| `conversation-send.yaml` | type body → tap send → message appears optimistically → still present after a 2s wait (round-trip succeeded). |
| `group-info.yaml` | open group conversation → tap header → member list shows seeded admin + member. |
| `group-add-member.yaml` | as admin, tap Add Member → multi-select friend → member appears in list. |
| `group-leave.yaml` | tap Leave Group → confirm sheet → conversation removed from `(tabs)/index`. |
| `friends-empty.yaml` | fresh user → "Find your friends" empty state visible. |
| `friends-list.yaml` | seed accepted friend → row renders with display name + presence dot. |
| `friends-add.yaml` | search by username → request sent → "Request sent" toast visible. |
| `friends-accept.yaml` | inbound request row → tap Accept → row promotes to friends list. |
| `friends-decline.yaml` | inbound request row → tap Decline → row disappears. |
| `friends-block.yaml` | friend row context menu → Block → row gone from friends, present on `settings/blocked`. |
| `friends-unblock.yaml` | `settings/blocked` → tap Unblock → row gone. |
| `presence-set.yaml` | profile tab → long-press avatar → choose DND → red dot + "Do Not Disturb" caption render. |
| `notifications-toggle.yaml` | settings/notifications → toggle "Friend requests" off → assertVisible the disabled state, refetch confirms persisted. |
| `devices-list.yaml` | settings/devices → seeded token row visible → tap Revoke → row disappears. |
| `theme-picker.yaml` | settings/theme → tap a non-default scheme swatch → root view re-paints (assertion: scheme name persists in AsyncStorage on relaunch). |
| `account-edit.yaml` | settings/account → change display name → save → `(tabs)/profile` shows new name. |
| `account-delete.yaml` | settings/account → Delete → confirm → re-enter password → `(auth)/login` visible. |
| `account-logout.yaml` | settings/account → Logout → `(auth)/login` visible + cookie cleared (re-launching does not auto-auth). |
| `profile-edit.yaml` | settings/profile-edit → bio + status emoji → save → profile renders both. |
| `search.yaml` | type "wak" in search modal → users / conversations / messages results all render. |
| `event-banner.yaml` | seeded WS message arrives while not on conversation screen → banner appears → tap → routes to thread. |
| `force-upgrade.yaml` | mock `/v1/healthz` → set `min_client_version` ahead of `expoConfig.version` → blocking modal appears + cannot dismiss. |
| `network-banner.yaml` | toggle airplane mode (Maestro `setAirplaneMode`) → `<NetworkBanner>` appears within 1s. |
| `admin-list.yaml` | seeded admin → admin tab → user list visible. |
| `admin-user-detail.yaml` | tap a user row → role/lock fields visible + impersonate button enabled. |
| `admin-impersonate-start-end.yaml` | start impersonation → `<ImpersonationBanner>` visible (driven by `impersonated_by` on `GET /v1/auth/me`) → End → banner gone. |
| `admin-audit.yaml` | audit tab → seeded action rows render with actor / action / target / timestamp. |

**Call flows** (require LiveKit container; gated behind `MAESTRO_LIVEKIT=1`):

| File | Asserts |
|---|---|
| `call-incoming-ring.yaml` | seeded WS `room.started` → CallKit / Android-equivalent ring → tap Accept → `<CallOverlay>` visible. |
| `call-toggle-video.yaml` | in active call → tap camera → tile shows video; tap again → back to avatar. |
| `call-pip.yaml` | navigate away from call → corner bubble snaps in; tap → returns to full overlay. |
| `call-decline.yaml` | incoming ring → tap Decline → ring stops, no overlay. |

Each flow file ends with at least one `assertVisible:` so a silent failure is caught by `bunx maestro test .maestro/`. Screenshots are taken at the final assertion point and uploaded to the PR by the per-milestone CI workflow (§13.7).

### 12.8 Flow assertion conventions

- **`assertVisible: "literal text"`** is preferred over `assertVisible: { id: "test-id" }` — the literal text is what the operator sees on the QR review, so the flow asserts the same thing.
- **`tapOn:` uses accessibility labels**, not test ids. Every interactive element has `accessibilityLabel` set in JSX (`<Button accessibilityLabel="Send" />`); flows tap the label. This survives copy edits worse than hard-coded strings, but it pays back during the §10 accessibility audit because the labels are already there.
- **Time-sensitive checks use `extendedWaitUntil:` with a generous timeout** (10s default). The operator's network is sometimes slow on cell — flaky flows are worse than slow flows.
- **State pollution between flows is forbidden.** `clearState: true` runs at the top of every screen-level flow. Sub-flows don't clear state; their parents do. Cross-flow seeds go through API setup, not by chaining UI flows.
- **Screenshots go in `.maestro/screenshots/<flow-name>/`** and are committed alongside the flow. CI compares the latest screenshot to the committed one and posts the diff in the PR — operator-visible regressions get caught at review time, not in the wild.

---

## 13. Tooling & config

### 13.1 `app.json` essentials

```json
{
  "expo": {
    "name": "Wakeup",
    "slug": "wakeup",
    "scheme": "wakeup",
    "version": "1.0.0",
    "orientation": "portrait",
    "userInterfaceStyle": "automatic",
    "newArchEnabled": true,
    "icon": "./assets/icons/daylight.png",
    "splash": {
      "image": "./assets/splash/daylight.png",
      "resizeMode": "contain",
      "backgroundColor": "#FAFAFA"
    },
    "ios": {
      "bundleIdentifier": "app.wakeup.client",
      "buildNumber": "1",
      "associatedDomains": ["applinks:wakeup.app"],
      "infoPlist": {
        "UIBackgroundModes": ["voip", "audio", "remote-notification"],
        "NSCameraUsageDescription": "Required to share video on calls.",
        "NSMicrophoneUsageDescription": "Required to talk on calls.",
        "NSFaceIDUsageDescription": "Used to unlock the app.",
        "NSPhotoLibraryUsageDescription": "Used to set your profile picture.",
        "NSUserTrackingUsageDescription": "We don't track you across other apps; this prompt is required by Apple.",
        "ITSAppUsesNonExemptEncryption": false,
        "CFBundleAlternateIcons": {
          "sunrise":  { "CFBundleIconFiles": ["icon-sunrise"],  "UIPrerenderedIcon": false },
          "daylight": { "CFBundleIconFiles": ["icon-daylight"], "UIPrerenderedIcon": false },
          "noon":     { "CFBundleIconFiles": ["icon-noon"],     "UIPrerenderedIcon": false },
          "golden":   { "CFBundleIconFiles": ["icon-golden"],   "UIPrerenderedIcon": false },
          "meadow":   { "CFBundleIconFiles": ["icon-meadow"],   "UIPrerenderedIcon": false },
          "dusk":     { "CFBundleIconFiles": ["icon-dusk"],     "UIPrerenderedIcon": false },
          "twilight": { "CFBundleIconFiles": ["icon-twilight"], "UIPrerenderedIcon": false },
          "aurora":   { "CFBundleIconFiles": ["icon-aurora"],   "UIPrerenderedIcon": false },
          "midnight": { "CFBundleIconFiles": ["icon-midnight"], "UIPrerenderedIcon": false },
          "rem":      { "CFBundleIconFiles": ["icon-rem"],      "UIPrerenderedIcon": false }
        }
      },
      "privacyManifests": {
        "NSPrivacyAccessedAPITypes": [
          { "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryUserDefaults",
            "NSPrivacyAccessedAPITypeReasons": ["CA92.1"] },
          { "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryFileTimestamp",
            "NSPrivacyAccessedAPITypeReasons": ["C617.1"] },
          { "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategorySystemBootTime",
            "NSPrivacyAccessedAPITypeReasons": ["35F9.1"] },
          { "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryDiskSpace",
            "NSPrivacyAccessedAPITypeReasons": ["E174.1"] }
        ]
      }
    },
    "android": {
      "package": "app.wakeup.client",
      "versionCode": 1,
      "permissions": [
        "android.permission.CAMERA",
        "android.permission.RECORD_AUDIO",
        "android.permission.USE_BIOMETRIC",
        "android.permission.FOREGROUND_SERVICE",
        "android.permission.FOREGROUND_SERVICE_PHONE_CALL",
        "android.permission.READ_PHONE_STATE",
        "android.permission.MANAGE_OWN_CALLS",
        "android.permission.POST_NOTIFICATIONS"
      ],
      "intentFilters": [
        {
          "action": "VIEW",
          "autoVerify": true,
          "data": [{ "scheme": "https", "host": "wakeup.app", "pathPrefix": "/" }],
          "category": ["BROWSABLE", "DEFAULT"]
        }
      ]
    },
    "plugins": [
      "expo-router",
      "expo-notifications",
      ["expo-local-authentication", { "faceIDPermission": "Used to unlock the app." }],
      ["expo-image-picker", { "photosPermission": "Used to set your profile picture." }],
      "expo-tracking-transparency",
      ["@sentry/react-native/expo", { "organization": "wakeup", "project": "mobile" }],
      "@livekit/react-native-expo-plugin",
      "react-native-callkeep",
      "react-native-android-widget",
      ["@expo/ui/swift-ui", { "targets": ["widgets/ios"] }]
    ],
    "updates": {
      "url": "https://u.expo.dev/<project-id>",
      "fallbackToCacheTimeout": 0
    },
    "runtimeVersion": { "policy": "appVersion" }
  }
}
```

Notes on the manifest above:
- `UIBackgroundModes` adds `remote-notification` so PushKit can wake the app for VoIP pushes.
- `CFBundleAlternateIcons` matches §10.5 — one entry per non-default scheme. Per-icon PNGs ship under `apps/mobile/ios/Wakeup/icon-<scheme>.png` via the `assets/icons/<scheme>.png` source.
- `privacyManifests` populates the `PrivacyInfo.xcprivacy` file Apple parses at submission. Re-audit on every dependency add.
- Android `intentFilters` enables universal links to `https://wakeup.app/...`; the actual route mapping happens in `app/(deep-links)/_layout.tsx` via Expo Router.
- The CallKit foreground-service permissions are declared so `react-native-callkeep` can register a `ConnectionService` on Android.

### 13.2 `eas.json` profiles

Three profiles: `development` (with dev client, points at local backend), `preview` (internal testers, points at staging), `production` (App Store / Play Store, points at prod).

### 13.3 `.env` files

- `.env.development` — `API_BASE_URL=http://localhost:8080`, `WS_BASE_URL=ws://localhost:8080`, `EXPO_PROJECT_ID=…`
- `.env.preview` — staging URLs.
- `.env.production` — prod URLs.

`lib/env.ts` reads via `process.env.EXPO_PUBLIC_API_BASE_URL` etc. (Expo strips `EXPO_PUBLIC_` prefix at build time.)

### 13.4 NativeWind setup

- Install per the `expo-tailwind-setup` skill's docs.
- `tailwind.config.ts` extends the default palette with our 10 themed token tables.
- `global.css` imports Tailwind base + per-scheme `@theme` blocks gated by a `[data-theme="…"]` attribute on the root view (NativeWind v5's mechanism).

### 13.5 Justfile additions (root repo)

The existing `justfile` already has `gen-client` (which writes `apps/mobile/lib/api/schema.ts`). Add:

```
# Mobile dev — local LAN only (faster, but only works when phone is on
# the same Wi-Fi as the laptop)
mobile-dev:
    cd apps/mobile && bunx expo start

# Mobile dev — tunneled. Use this for the operator-review loop so the
# phone can connect over cell data or a different network.
mobile-tunnel:
    cd apps/mobile && bunx expo start --tunnel

mobile-ios:
    cd apps/mobile && bunx expo run:ios

mobile-android:
    cd apps/mobile && bunx expo run:android

# Generate Orval hooks from the typed schema
mobile-gen-hooks:
    cd apps/mobile && bunx orval

# Run all Maestro flows
mobile-test-flows:
    cd apps/mobile && maestro test .maestro/

# Type-check + lint (matches CI)
mobile-verify: mobile-gen-hooks
    cd apps/mobile && bunx tsc --noEmit
    cd apps/mobile && bunx eslint . --max-warnings 0
```

### 13.6 EAS Update channels

- `production` → `eas update --channel=production` after every commit to `main`.
- `preview` → `eas update --channel=preview` for feature branches.
- `development` → uses dev client; updates not relevant.

### 13.7 GitHub Actions additions

Adds `.github/workflows/mobile.yml` running on changes to `apps/mobile/**`:
- `bun install`
- `just gen-client && just mobile-gen-hooks` (regenerate schema + hooks)
- `bunx tsc --noEmit`
- `bunx eslint .`
- (Maestro flows run in EAS Build's preview, not on every Action — Actions doesn't have a simulator.)

---

## 14. Bootstrap order

This is the literal first-run sequence for the operator after `WAKEUP.md` Phase 14 has stood up the backend.

### 14.1 Operator one-time setup (do this once, before milestone work begins)

- [ ] **Install Expo Go** on the operator's phone (App Store / Play Store).
- [ ] **Install Maestro CLI**: `curl -Ls "https://get.maestro.mobile.dev" | bash`. Verify `maestro --version`.
- [ ] **Add the Maestro MCP** to the implementer's Claude Code config so flows can be driven from the implementer's loop. Reference: https://maestro.mobile.dev/. The MCP exposes `mobile_take_screenshot`, `mobile_run_flow`, `mobile_view_hierarchy`, etc.
- [ ] **Confirm the operator's phone scans an Expo QR**: in any random `bunx create-expo-app` directory run `bunx expo start --tunnel`, scan the QR. The "Welcome to Expo" screen should load.
- [ ] **Install the iOS Simulator + Android Emulator** locally so the Maestro MCP has something to drive.

### 14.2 Implementer sequence (Phase 0 onward in §16)

1. **Initialise the Expo app.** `cd apps/mobile && bunx create-expo-app@latest --template tabs`. Move the generated files to match §2. Commit.
2. **Install the locked stack** from §3 in one shot. `bun add` the runtime deps; `bun add -D` the dev deps.
3. **Wire NativeWind v5** per the `expo-tailwind-setup` skill. Add `global.css`, `tailwind.config.ts`. Add the 10-scheme tokens in §4.5. Smoke-test by changing the root view background between two schemes.
4. **Install ALL RNR components and auth blocks** per §3.1 in one batch. `npx @react-native-reusables/cli@latest init` then `npx @react-native-reusables/cli@latest add <every-name-in-§3.1>`. Verify a Button + a Sign-In form render with theme tokens.
5. **Generate API types + hooks.** `just gen-client` writes `lib/api/schema.ts`. Add `lib/api/orval.config.ts`. Run `just mobile-gen-hooks` → `lib/api/hooks/*.ts`. Verify a sample query compiles.
6. **Wire the auth screens** per §5.1, using RNR's prebuilt forms (`sign-in-form`, `sign-up-form`, `forgot-password-form`, `reset-password-form`). Login + Register first; password reset can wait.
7. **Mount the providers** in `app/_layout.tsx`: QueryClientProvider → ThemeProvider → BiometricGate → CallOverlay → ToastRoot → Stack. Order matters (Theme outside Toast so toasts use theme colours).
8. **Conversation list + thread**, then composer with optimistic send.
9. **WebSocket** per §4.4 with the dispatcher mapping in §6.2.
10. **Friends tab**.
11. **Push notifications** registration + handlers.
12. **Call screens** + LiveKit wiring.
13. **Theme picker + biometric lock**.
14. **Widgets** (last because they need real backend data flowing).
15. **EAS configuration**, channels, OTA pipeline.

---

## 15. Workflow rules (per milestone)

For every checked milestone in §16:

1. Read the milestone's spec section.
2. **Check the relevant Expo skill** (see §15.1 below). The Claude Code session has the `expo:*` plugin installed; consult its skills before writing code that overlaps a documented Expo concern (data fetching, native UI, OTA updates, deployment, widgets, etc.). Skill lookup is free; reinventing what the skill documents is not.
3. Implement.
4. Add or extend the Maestro flow.
5. Run `just mobile-verify` until clean (type-check + lint).
6. Run the Maestro flow via the Maestro MCP. Capture screenshots.
7. Run `just mobile-tunnel` and post the QR code in the conversation. **Stop. Wait for the operator to scan + review on their phone.** Don't proceed without explicit "looks good."
8. Apply corrections from the operator's review.
9. Commit with the documented message.
10. Open PR. Resolve CodeRabbit feedback.
11. Squash-merge.

Two non-negotiables in this loop: (a) the Expo skill consultation in step 2 — every milestone touches at least one — and (b) the QR-scan review in step 7. Skipping either is a process violation.

### 15.1 Expo skills cheat-sheet

The `expo` plugin gives the implementer these skills (use the `Skill` tool to invoke). Pick them off as the milestones in §16 surface the relevant concern:

| Skill | Use during |
|---|---|
| `expo:expo-tailwind-setup` | Phase 1 (NativeWind wiring) |
| `expo:building-native-ui` | Phases 4, 5, 6, 11 (any UI-heavy screen) |
| `expo:native-data-fetching` | Phase 2 (fetcher, cache, error handling) — covers TanStack Query patterns |
| `expo:expo-dev-client` | Phase 0.6 if a custom dev client is needed (e.g. for native modules Expo Go can't load) |
| `expo:expo-module` | Phase 9 if LiveKit's RN package requires a config plugin tweak we haven't anticipated |
| `expo:expo-ui-swift-ui` | Phase 12.1 (iOS widget) |
| `expo:expo-ui-jetpack-compose` | Phase 12.2 (Android widget) |
| `expo:eas-update-insights` | Phase 13 (OTA rollout health) |
| `expo:expo-cicd-workflows` | Phase 0.8 (CI workflow) |
| `expo:expo-deployment` | Phase 13 (App Store / Play Store config), Phase 11.5 (privacy manifest), Phase 11.6 (store metadata) |
| `expo:upgrading-expo` | Reserved for v1 → v1.1 SDK bumps |
| `expo:use-dom` | Reserved for v2 (any web-only screen) |
| `expo:expo-api-routes` | Reserved — we don't ship any Expo API routes for v1 |

**Expectation:** when a milestone matches a skill, the implementer invokes the skill *first* and references its guidance in the commit body if the answer was non-obvious. The cost of skipping a skill is reproducing knowledge it would have given for free.

---

## 16. Sequential build checklist

### Phase 0 — Repository setup

- [ ] **0.0** Operator one-time setup per §14.1 complete: Expo Go on phone, Maestro CLI installed, Maestro MCP wired into Claude Code, simulators ready. (No commit; this is operator state, not repo state.)
- [ ] **0.1** `bunx create-expo-app@latest apps/mobile --template tabs`. Move files to match §2 layout. Confirm `bun install && bunx expo start --tunnel` boots the default screens on iOS Simulator AND on the operator's phone via Expo Go (operator scans the QR; reviews; approves).
  - Commit: `feat(mobile): bootstrap expo app`
- [ ] **0.2** Install all dependencies from §3 in `package.json`. Pin major versions. `bun install` clean.
  - Commit: `chore(mobile): lock dependency stack`
- [ ] **0.3** `tsconfig.json` strict, `@/*` path alias, `expo/tsconfig.base` extended.
  - Commit: `chore(mobile): configure typescript strict + path alias`
- [ ] **0.4** `eslint-config-expo` + `prettier` configured. `bunx eslint . --max-warnings 0` clean on the boilerplate.
  - Commit: `chore(mobile): add eslint and prettier configs`
- [ ] **0.5** `.maestro/` directory with `README.md` describing the operator-review loop and a sample `flows/login.yaml` sub-flow stub. Verify `maestro test .maestro/` runs on an empty flow set.
  - Commit: `chore(mobile): add maestro scaffolding`
- [ ] **0.6** `app.json` per §13.1 — bundle ids, scheme, plugins (`expo-router`, `expo-notifications`, `expo-local-authentication`, LiveKit, `react-native-android-widget`, `@expo/ui/swift-ui`), permissions, background modes.
  - Commit: `chore(mobile): configure app.json with permissions and plugins`
- [ ] **0.7** `eas.json` with three profiles per §13.2.
  - Commit: `chore(mobile): add eas.json with three build profiles`
- [ ] **0.8** GitHub Actions workflow per §13.7. Consult the `expo:expo-cicd-workflows` skill for EAS-aware steps. Initial run green on the boilerplate.
  - Commit: `ci(mobile): add type-check + lint workflow`

### Phase 1 — Theming + foundation

- [ ] **1.1** NativeWind v5 wired per the `expo-tailwind-setup` skill. `global.css`, `tailwind.config.ts` in place.
  - Commit: `feat(mobile): wire nativewind v5`
- [ ] **1.2** Add the 10 sleep-cycle schemes from §4.5 to `tailwind.config.ts`. Each scheme has its `@theme` block. Smoke-test by switching the root via a debug keystroke.
  - Commit: `feat(mobile): add ten sleep-cycle color schemes`
- [ ] **1.3** Theme store (`lib/theme/store.ts`) + provider (`lib/theme/provider.tsx`). AsyncStorage persistence. `system` follows `Appearance.getColorScheme()`.
  - Commit: `feat(mobile): add theme store with async-storage persistence`
- [ ] **1.4** Install **all** RNR components and auth blocks per §3.1 in one batch (~30 components + 5 auth blocks). The set is intentionally complete because RNR is copy-in, not a runtime dependency. Verify a `<Button>`, `<Card>`, and the `<SignInForm>` block render with theme tokens applied. Operator scans the QR and reviews the gallery.
  - Commit: `feat(mobile): install react-native-reusables foundation`
- [ ] **1.5** `<EmptyState>` + `<Skeleton>` wrappers in `components/ui/`.
  - Commit: `feat(mobile): add empty-state and skeleton primitives`
- [ ] **1.6** Toast wrapper (`lib/toast.ts`) per §4.6. `ToastRoot` mounted at root layout.
  - Commit: `feat(mobile): wire burnt toast wrapper`
- [ ] **1.7** Haptics wrapper (`lib/haptics.ts`) per §4.11 with three preset shapes (`tap`, `success`, `warning`). Unit-test via mock.
  - Commit: `feat(mobile): add haptics wrapper`
- [ ] **1.8** Sentry init in `app/_layout.tsx` per §4.10. DSN read from `EXPO_PUBLIC_SENTRY_DSN`. Test crash button verifies it lands.
  - Commit: `feat(mobile): wire sentry crash and error reporting`
- [ ] **1.9** `<RootErrorBoundary>` mounted at root. Triggers a fallback render + reports to Sentry. Manual smoke: throw inside a screen, confirm fallback shows.
  - Commit: `feat(mobile): add root error boundary`
- [ ] **1.10** `lib/network/state.ts` + `<NetworkBanner>` mounted at root. Toggle airplane mode → banner appears within 1s.
  - Commit: `feat(mobile): add network state hook and offline banner`
- [ ] **1.11** Swap RN `<Image>` import → `expo-image` `<Image>` everywhere via codemod. ESLint rule `no-restricted-imports` blocks future RN `<Image>` use.
  - Commit: `feat(mobile): standardise on expo-image`
- [ ] **1.12** `<FlashList>` adopted as the list primitive — wrap in `components/ui/List.tsx` so the conversation list and message list both pass through. ESLint rule blocks bare `<FlatList>`.
  - Commit: `feat(mobile): adopt flash-list as default list primitive`

### Phase 2 — API client

- [ ] **2.1** `just gen-client` produces `lib/api/schema.ts`. Confirm the file compiles with `tsc --noEmit`.
  - Commit: `chore(mobile): generate openapi schema types`
- [ ] **2.2** `lib/api/client.ts` fetcher with cookie support, idempotency-key injection, error → toast mapping.
  - Commit: `feat(mobile): add api fetch wrapper with idempotency keys`
- [ ] **2.3** Orval config (`lib/api/orval.config.ts`) wired to the generated schema. `bunx orval` produces `lib/api/hooks/*.ts`. CI runs this in `mobile-verify`.
  - Commit: `feat(mobile): generate react-query hooks via orval`
- [ ] **2.4** `useIdempotencyKey()` helper, unit-tested.
  - Commit: `feat(mobile): add idempotency key hook`
- [ ] **2.5** Mutation retry config per §4.10 (4xx never retries, 5xx + network up to 3× with exp backoff, idempotency-key reused). Unit-tested with mocked failures.
  - Commit: `feat(mobile): add offline-aware mutation retry config`
- [ ] **2.6** TanStack Query `persistQueryClient` wired to AsyncStorage (`query-cache:v1`, `mutation-cache:v1`). Test: queue a mutation offline, kill app, reopen, confirm replay on reconnect.
  - Commit: `feat(mobile): persist query and mutation caches for offline replay`
- [ ] **2.7** `/v1/healthz` poll on every authenticated foreground; `<ForceUpgradeGate>` blocks the app when `min_client_version > current`. Manual test: spoof a higher min via env override.
  - Commit: `feat(mobile): add force-upgrade gate via healthz`

### Phase 3 — Auth screens

- [ ] **3.0** `(onboarding)/index.tsx` three-screen carousel (welcome / friends value-prop / notifications permission). `<OnboardingCarousel>` + AsyncStorage `onboarding:complete`. Maestro flow `onboarding.yaml`. The third screen must call `Notifications.requestPermissionsAsync()` before letting the user advance.
  - Commit: `feat(mobile): add first-launch onboarding carousel`
- [ ] **3.1** `(auth)/_layout.tsx` stack, no tab bar. `(auth)/login.tsx` form using RNR Input + Button. `useLogin` mutation. Maestro flow `login.yaml`.
  - Commit: `feat(mobile): add login screen`
- [ ] **3.2** `(auth)/register.tsx` form. Maestro flow `register.yaml`.
  - Commit: `feat(mobile): add register screen`
- [ ] **3.3** `(auth)/forgot.tsx` + `(auth)/reset.tsx`. Universal-link config in `app.json` for the password-reset deep link.
  - Commit: `feat(mobile): add password reset flow with deep link`
- [ ] **3.4** `useGetMe()` integrated into root: returns 401 → redirect to `(auth)/login`. Maestro flow `auth-redirect.yaml`.
  - Commit: `feat(mobile): wire auth-state redirect`

### Phase 4 — Tabs + Friends

- [ ] **4.1** `(tabs)/_layout.tsx` with three tabs + lucide icons.
  - Commit: `feat(mobile): scaffold tab navigator`
- [ ] **4.2** `(tabs)/friends.tsx` rendering accepted friends + incoming/outgoing requests. `<FriendRow>` + `<PresenceDot>`. Maestro flow `friends-list.yaml`.
  - Commit: `feat(mobile): build friends tab`
- [ ] **4.3** Add-friend flow: search by username (debounced `GET /v1/users?q=`), `useSendFriendRequest`. Maestro flow `friends-add.yaml`.
  - Commit: `feat(mobile): add friend search and request flow`
- [ ] **4.4** Accept / decline / unfriend / block / unblock actions wired. Maestro flows for each. Friend-accept triggers `haptics.success()`.
  - Commit: `feat(mobile): wire friend request actions`
- [ ] **4.5** Pull-to-refresh on the friends tab (`<PullToRefresh>` wrapper). Past-threshold tap fires `haptics.tap()`.
  - Commit: `feat(mobile): add pull-to-refresh on friends tab`

### Phase 5 — Conversations list

- [ ] **5.1** `(tabs)/index.tsx` rendering the conversations list, sorted by `last_message_at`. Empty state. Maestro flow `conversations-empty.yaml`.
  - Commit: `feat(mobile): build conversations list screen`
- [ ] **5.2** "+" button on the conversations tab → `conversation/new.tsx` modal. Multi-select friends + group name. `useCreateConversation`. Maestro flow `conversation-create.yaml`.
  - Commit: `feat(mobile): add new-conversation flow`
- [ ] **5.3** Tap a friend in `(tabs)/friends` → DM auto-create on first message. Helper: `useEnsureDirectConversation(friendId)`.
  - Commit: `feat(mobile): auto-create dm on first message`
- [ ] **5.4** Pull-to-refresh on the conversations list. Past-threshold tap fires `haptics.tap()`.
  - Commit: `feat(mobile): add pull-to-refresh on conversations list`
- [ ] **5.5** Global search modal (`search` route) — debounced 200ms across friends + conversations + (later) messages. Maestro flow `search.yaml`.
  - Commit: `feat(mobile): add global search`
- [ ] **5.6** Pin / mute long-press menu on conversation rows. `<PinToggle>` + `<MuteSheet>` per §5.2. Optimistic resort on pin. Maestro flow `conv-pin-mute.yaml`.
  - Commit: `feat(mobile): add pin and mute on conversations`
- [ ] **5.7** `conversation/[id]/info.tsx` — group info / DM profile. Tappable conversation header opens it. Group admins get Add Member (`POST /v1/conversations/{id}/members`) + Remove Member (`DELETE /v1/conversations/{id}/members/{user_id}`); every member gets Leave Group (`DELETE /v1/conversations/{id}`). DM variant renders the peer's profile only (no member list, no leave). Maestro flows: `group-info.yaml`, `group-add-member.yaml`, `group-leave.yaml`.
  - Commit: `feat(mobile): add group info screen with add member and leave`

### Phase 6 — Conversation thread

- [ ] **6.1** `conversation/[id].tsx` rendering messages via `<MessageList>` (gifted-chat shell). Infinite query with cursor pagination from `GET /v1/conversations/{id}/messages`. Maestro flow `conversation-thread.yaml`.
  - Commit: `feat(mobile): render conversation thread with cursor pagination`
- [ ] **6.2** `<Composer>` with optimistic send. `useSendMessage` mutation per §4.8. Replace placeholder by id on `onSuccess`. Tapping send fires `haptics.tap()`; failed send fires `haptics.warning()`.
  - Commit: `feat(mobile): add composer with optimistic send`
- [ ] **6.3** `MarkRead` on screen focus. Read-receipts surfaced as small dots under bubbles in groups.
  - Commit: `feat(mobile): wire read receipts`
- [ ] **6.4** Typing indicator (publish on input change, debounce-stop on idle 3s, render incoming `typing.start`/`typing.stop` from WS).
  - Commit: `feat(mobile): wire typing indicator`
- [ ] **6.5** Long-press a `<ChatBubble>` opens `<MessageContextMenu>` (Copy, React stub, Report, Delete-mine). Long-press fires `haptics.tap()`. Maestro flow `message-context-menu.yaml`.
  - Commit: `feat(mobile): add long-press message context menu`

### Phase 7 — WebSocket integration

- [ ] **7.1** `lib/ws/client.ts` singleton with exp backoff reconnect. `useWSConnectionState()` hook.
  - Commit: `feat(mobile): add websocket client with backoff reconnect`
- [ ] **7.2** Lifecycle hooks: connect on app foreground + auth, disconnect on background > 30s + on logout.
  - Commit: `feat(mobile): wire websocket lifecycle`
- [ ] **7.3** Dispatcher mapping every event from §6.2 to a Query Cache action. Unit-tested per event.
  - Commit: `feat(mobile): map ws events to react-query cache`
- [ ] **7.4** Reconnect banner on the conversation screen. "Reconnected" toast on recovery.
  - Commit: `feat(mobile): surface ws connection state in ui`
- [ ] **7.5** `<EventBanner>` per §4.13. Root-mounted singleton with a Zustand queue. The DISPATCHER (`lib/ws/dispatcher.ts`) makes the enqueue/skip decision — the banner component never filters. Enqueue: `message.new` (when not on the conversation screen), `friend.request_received`, `friend.request_accepted`, `conversation.member_added` (caller is the newly added member). Skip: conversation muted, presence intent dnd, on the conversation screen for that `message.new`, or the event is `room.started`. Tap routes to the relevant screen; swipe-up dismisses; 4-second auto-dismiss; light haptic on appearance. Maestro flow `event-banner.yaml`. Same commit: drop the legacy toast on `friend.request_received` from §6.2's dispatcher table — the banner subsumes it.
  - Commit: `feat(mobile): add in-app event banner for foreground notifications`

### Phase 8 — Push notifications

- [ ] **8.1** `lib/push/register.ts` requests permission + registers Expo token + POSTs `/v1/devices`. AsyncStorage caches `device:registered`.
  - Commit: `feat(mobile): register expo push token`
- [ ] **8.2** Foreground / background handlers per §7.3. Routes to the right screen on tap.
  - Commit: `feat(mobile): handle push notification taps`
- [ ] **8.3** Settings → Notifications. Category toggles bound to `PATCH /v1/users/me/notifications`. Maestro flow `notifications-toggle.yaml`.
  - Commit: `feat(mobile): build notification preferences screen`
- [ ] **8.4** Settings → Devices. List + revoke registered tokens. Maestro flow `devices-list.yaml`.
  - Commit: `feat(mobile): build devices management screen`
- [ ] **8.5** `INCOMING_CALL` notification category with Accept / Decline action buttons (iOS) and Android equivalents.
  - Commit: `feat(mobile): add incoming-call notification actions`
- [ ] **8.6** Notification thread-id grouping per §7.5. Verify on a real device: send 5 messages from one conversation while app is backgrounded → one collapsed group, not 5 banners.
  - Commit: `feat(mobile): group notifications by thread id`
- [ ] **8.7** App icon badge count from WS heartbeat `unread_total` per §7.5. Optimistic decrement on `MarkRead`. `X-Unread-Total` header consumed at launch.
  - Commit: `feat(mobile): wire unread badge count`

### Phase 9 — Voice & video

- [ ] **9.1** `lib/call/store.ts` Zustand state machine (`idle | dialing | active | pip`). Unit tests for transitions.
  - Commit: `feat(mobile): add call state machine`
- [ ] **9.2** `lib/call/room.ts` LiveKit Room singleton. Permission pre-flight.
  - Commit: `feat(mobile): wire livekit room singleton`
- [ ] **9.3** `<RoomBanner>` at top of conversation. Three states. Maestro flow `room-banner-states.yaml`.
  - Commit: `feat(mobile): build room banner with three states`
- [ ] **9.4** `<CallOverlay>` full-screen mode: `<ParticipantTile>` grid + `<ControlBar>`. Maestro flow `call-fullscreen.yaml`.
  - Commit: `feat(mobile): build full-screen call overlay`
- [ ] **9.5** `<DraggablePip>` corner-snapping bubble. Reanimated gesture. Maestro flow `call-pip.yaml`.
  - Commit: `feat(mobile): add draggable pip bubble`
- [ ] **9.6** Speaking indicator pulse on `<ParticipantTile>`.
  - Commit: `feat(mobile): wire speaking indicator`
- [ ] **9.7** Mid-call mic + camera toggles wired through LiveKit's `localParticipant`. Backend's `room.video_changed` WS event reflected in remote tiles.
  - Commit: `feat(mobile): wire mic and camera toggles`
- [ ] **9.8** `react-native-incall-manager` for speakerphone routing on connect, ear-piece on proximity sensor.
  - Commit: `feat(mobile): add audio routing via incall-manager`
- [ ] **9.9** Leave-call flow + lone-kick UX. "Call ended — everyone else left" toast on backend-driven `room.ended`. Decline fires `haptics.warning()`.
  - Commit: `feat(mobile): handle leave + lone-kick gracefully`
- [ ] **9.10** `react-native-callkeep` setup per §8.6. iOS: PushKit token registration → POST `/v1/devices/voip`. Android: `ConnectionService` registration. Bridge `answerCall` / `endCall` events to the LiveKit room.
  - Commit: `feat(mobile): integrate callkit and connectionservice`
- [ ] **9.11** End-to-end VoIP test on a real device: force-quit the app, have a second account call, verify the iOS lock screen shows the full CallKit ring UI, accepting joins the LiveKit room cleanly.
  - Commit: `test(mobile): verify callkit lock-screen ring path`

### Phase 10 — Theme picker + biometric lock

- [ ] **10.1** `settings/theme.tsx` 11-swatch picker (10 + system) using lucide icons. Maestro flow `theme-pick.yaml`. Switching the scheme fires `haptics.success()`.
  - Commit: `feat(mobile): build theme picker`
- [ ] **10.2** `lib/biometric/store.ts` toggle + last-unlock + lock-after duration. AsyncStorage persisted.
  - Commit: `feat(mobile): add biometric lock store`
- [ ] **10.3** `<BiometricGate>` mounted at root. `expo-local-authentication` prompt on foreground after timeout. Maestro flow `biometric-gate.yaml` (sim flow tested with mocked `LocalAuthentication`). Successful unlock fires `haptics.success()`.
  - Commit: `feat(mobile): build biometric gate overlay`
- [ ] **10.4** `settings/privacy.tsx` toggle + lock-after picker. Maestro flow `privacy-toggle.yaml`.
  - Commit: `feat(mobile): build privacy and security settings`
- [ ] **10.5** `<AppIconSwitcher>` wired into the theme picker. Tapping a scheme on iOS calls the alternate-icon API. Verify on simulator + real device.
  - Commit: `feat(mobile): switch app icon to match scheme`
- [ ] **10.6** `<SplashScreenProvider>` reads persisted scheme synchronously before React mount; applies the matching splash image. Per-scheme splash assets shipped under `assets/splash/<scheme>.png`.
  - Commit: `feat(mobile): per-scheme splash screen`

### Phase 11 — Profile + remaining settings

- [ ] **11.1** `(tabs)/profile.tsx` with "me" card and entry to all settings sub-screens.
  - Commit: `feat(mobile): build profile tab`
- [ ] **11.2** `settings/account.tsx` — display name, avatar (uses `expo-image-picker` + `expo-image-manipulator` to compress to ≤1024px / 85% quality before `POST /v1/users/me/avatar`), password change, logout button calling `POST /v1/auth/logout` (clears all queries + cookie + navigates to `(auth)/login`).
  - Commit: `feat(mobile): build account settings with image compression`
- [ ] **11.3** `settings/blocked.tsx` — list of blocked users + unblock button per row. Maestro flow `blocked-list.yaml`.
  - Commit: `feat(mobile): build blocked-users management screen`
- [ ] **11.4** `settings/delete-account.tsx` — destructive flow per §10.5. Confirmation modal → password re-entry → `DELETE /v1/users/me` → clear local state → redirect. Maestro flow `delete-account.yaml`.
  - Commit: `feat(mobile): build account deletion flow`
- [ ] **11.5** `settings/about.tsx` — version, build, runtime, privacy + ToS links, 7-tap debug panel.
  - Commit: `feat(mobile): build about screen`
- [ ] **11.6a** `settings/profile-edit.tsx` — display name, bio (≤280 chars with live counter), `<StatusEmojiPicker>`, avatar. PATCH /v1/users/me + multipart avatar. Maestro flow `profile-edit.yaml`.
  - Commit: `feat(mobile): build profile edit with bio and status emoji`
- [ ] **11.6b** `user/[id].tsx` — view another user's public profile. Reuses `<FriendRow>` patterns; surfaces friend / message / block actions. Maestro flow `user-profile-view.yaml`.
  - Commit: `feat(mobile): build other-user profile view`
- [ ] **11.6c** `<PresencePicker>` bottom-sheet from profile tab (long-press own avatar OR explicit "Set status" button). Five options including DND. Persists via `POST /v1/presence/status`.
  - Commit: `feat(mobile): add manual presence picker with dnd`
- [ ] **11.6d** `settings/contacts.tsx` — first-run consent screen → `expo-contacts` permission → SHA-256 hash entries on device → `POST /v1/contacts/match` → render matched users + share-sheet invite for unmatched. Privacy copy: "We hash your contacts on this device and never send raw addresses." Maestro flow `contacts-sync.yaml`.
  - Commit: `feat(mobile): add email-based contact sync`

### Phase 11.4 — Admin tab (admin users only)

The backend's `/v1/admin/*` routes are already implemented. This phase is pure UI on top.

- [ ] **11.4.1** `(tabs)/admin.tsx` landing — three navigation cards (Users, Audit Log, Active Impersonation). `<AdminTabGuard>` redirects non-admins. Tab bar in `(tabs)/_layout.tsx` reads `useGetMe().data.role` and conditionally renders the fourth tab. Maestro flow `admin-tab-visibility.yaml` (covers both admin-sees-tab and user-doesn't-see-tab paths).
  - Commit: `feat(mobile): add admin tab gated by role`
- [ ] **11.4.2** `admin/users.tsx` — debounced search + paginated FlashList of users. `GET /v1/admin/users`. Maestro flow `admin-list.yaml`.
  - Commit: `feat(mobile): build admin users list`
- [ ] **11.4.3** `admin/user/[id].tsx` — user detail + admin actions (role change, lock/unlock). `<AlertDialog>` confirmation per action. `haptics.warning()` on confirm. Maestro flow `admin-user-detail.yaml`.
  - Commit: `feat(mobile): build admin user detail with role and lock controls`
- [ ] **11.4.4** Impersonation flow — `POST /v1/admin/users/{id}/impersonate` from user detail. `<ImpersonationBanner>` mounted at root, surfaces across all screens. End-impersonation calls `POST /v1/admin/impersonate/end` and reloads `useGetMe()`. Maestro flow `admin-impersonate-start-end.yaml`.
  - Commit: `feat(mobile): wire admin impersonation with global banner`
- [ ] **11.4.5** `admin/audit.tsx` — paginated audit log viewer with actor/target/action filters. Maestro flow `admin-audit.yaml`.
  - Commit: `feat(mobile): build admin audit log viewer`

### Phase 11.5 — Compliance & accessibility

- [ ] **11.5.1** Privacy manifest (`PrivacyInfo.xcprivacy`) per §10.5. Verify with `xcrun privacymanifest` (or the Xcode 15+ equivalent) that all Required Reasons API uses are declared.
  - Commit: `chore(mobile): add ios privacy manifest`
- [ ] **11.5.2** App Tracking Transparency prompt on first authenticated launch per §10.5. AsyncStorage `tracking:prompted` gate. Manual test: install fresh, observe prompt before conversation list.
  - Commit: `feat(mobile): show app tracking transparency prompt`
- [ ] **11.5.3** Universal links per §10.5. Configure `ios.associatedDomains` and `android.intentFilters`. Verify `https://wakeup.app/c/<id>` opens the right conversation. Backend serves `apple-app-site-association` and `assetlinks.json` (shipped in PR #104; set `IOS_APP_ID` / `ANDROID_PACKAGE` / `ANDROID_SHA256_FINGERPRINTS` in the deploy env so the manifests aren't 404).
  - Commit: `feat(mobile): wire universal links for conversations and users`
- [ ] **11.5.4** Accessibility baseline pass: every `<Pressable>` has `accessibilityLabel`, every screen passes `accessibilityRole`, Dynamic Type honored, reduced-motion respected. Manual audit using accessibility-inspector on iOS Simulator + TalkBack on Android Emulator. WCAG AA contrast checked per scheme.
  - Commit: `feat(mobile): accessibility baseline pass`

### Phase 11.6 — App store metadata

- [ ] **11.6.1** Create `apps/mobile/store/` per §10.5: iOS + Android directory structure, placeholder description / keywords / privacy URL files.
  - Commit: `chore(mobile): add app store metadata scaffolding`
- [ ] **11.6.2** Automate per-scheme screenshots via Maestro `takeScreenshot` driven by a `bun run screenshots` recipe. Output shaped to `store/<platform>/screenshots/<scheme>/<screen>.png`.
  - Commit: `chore(mobile): automate per-scheme store screenshots`
- [ ] **11.6.3** App icon source assets shipped: 11 1024×1024 PNGs at `assets/icons/<scheme>.png` (10 schemes + default). Splash assets at `assets/splash/<scheme>.png`.
  - Commit: `chore(mobile): add per-scheme icon and splash assets`

### Phase 12 — Widgets

- [ ] **12.1** `widgets/ios/` — SwiftUI WidgetKit target via `@expo/ui/swift-ui`. Calls `/v1/widget/friends`.
  - Commit: `feat(mobile): add ios friends widget`
- [ ] **12.2** `widgets/android/FriendsWidget.tsx` — `react-native-android-widget` worker.
  - Commit: `feat(mobile): add android friends widget`
- [ ] **12.3** Tapping a widget row deep-links into the corresponding DM. Maestro flow can't drive widgets; manual review only.
  - Commit: `feat(mobile): widget deep links into dm`

### Phase 13 — EAS + OTA

- [ ] **13.1** `eas.json` profiles audited. EAS project linked. First development build succeeds on iOS Simulator + Android Emulator.
  - Commit: `chore(mobile): link eas project + dev build`
- [ ] **13.2** `eas update --channel=preview` succeeds. The preview build pulls the update.
  - Commit: `chore(mobile): wire eas update preview channel`
- [ ] **13.3** `eas-update-insights` skill consulted for rollout health checks. Preview build pings home with a heartbeat update; verify it lands.
  - Commit: `chore(mobile): verify ota update health`

### Phase 14 — Final smoke + handoff

- [ ] **14.1** Run the operator's full smoke flow on iOS Simulator (every screen in §5.1).
  - Commit: `docs(mobile): smoke test ios`
- [ ] **14.2** Run the operator's full smoke flow on Android Emulator.
  - Commit: `docs(mobile): smoke test android`
- [ ] **14.3** Real-device smoke on iOS: install via TestFlight (preview channel), verify CallKit / VoIP push / lock-screen ring path end-to-end.
  - Commit: `docs(mobile): smoke test ios real device`
- [ ] **14.4** Real-device smoke on Android: install via Play Internal testing, verify ConnectionService / FCM data wake / system in-call UI.
  - Commit: `docs(mobile): smoke test android real device`
- [ ] **14.5** Force-upgrade gate verified by spoofing a higher `min_client_version` in staging — both real devices show the blocking modal.
  - Commit: `test(mobile): verify force-upgrade gate`
- [ ] **14.6** Account deletion verified end-to-end: create test account, delete, confirm subsequent login fails and conversation members see the redacted name.
  - Commit: `test(mobile): verify account deletion flow`
- [ ] **14.7** Final `just mobile-verify` green. Final CodeRabbit review clean.
  - Commit: `chore(mobile): v1 ready for stores`

---

## 17. Done criteria

The Expo client is **done** when, and only when, all of the following are true:

1. ✅ Every checkbox in §16 is checked.
2. ✅ `just mobile-verify` exits 0 (type-check + lint).
3. ✅ Every Maestro flow in `.maestro/` runs green.
4. ✅ Every screen in §5.1 has been operator-reviewed via the Maestro MCP.
5. ✅ Every API endpoint in `WAKEUP.md` §6 has at least one client call (verified by grepping `useGet*`/`useSet*` hook usage).
6. ✅ Every WebSocket event in `WAKEUP.md` §7.2 is wired in `lib/ws/dispatcher.ts`.
7. ✅ The 10 sleep-cycle schemes render correctly on iOS + Android. Switching schemes is instant. The picker shows the right lucide icon per scheme.
8. ✅ The biometric lock works on a real device for both Face ID and passcode fallback.
9. ✅ Push notifications round-trip on a real device: Send a message from a second test account → background the receiver → notification arrives → tap → opens the conversation. Multiple messages from the same conversation collapse into one notification group. Badge count reflects unread total.
10. ✅ Two real devices can voice-call each other end-to-end. Toggling video works. Picture-in-picture survives navigating to the friends tab and back.
11. ✅ The friends widget displays on both home screens. Tapping a row opens the DM.
12. ✅ EAS Update successfully ships a JS bundle change to the preview channel and the test device picks it up on next foreground.
13. ✅ Every screen-bearing milestone in §16 was reviewed by the operator via the Expo Go QR scan before commit (verifiable via PR description's "Operator review: ✓ <date>" line, which becomes a CI check in a later phase).
14. ✅ **CallKit / ConnectionService verified end-to-end on real devices** — force-quit the app on iOS, receive an incoming call, see the lock-screen full ring UI, accept, join the LiveKit room cleanly. Same on Android via ConnectionService.
15. ✅ **Privacy manifest passes Apple's submission validator.** No "Missing required reason API" warning at archive time.
16. ✅ **Account deletion ships and is reachable from `settings/account` in ≤ 2 taps** — Apple-required.
17. ✅ **App Tracking Transparency prompt fires on first authenticated launch** and is gated by `tracking:prompted` thereafter.
18. ✅ **Universal links work**: tapping `https://wakeup.app/c/<id>` from another app opens the conversation directly. Same for `/u/<username>`.
19. ✅ **Accessibility baseline passes**: VoiceOver / TalkBack reads every actionable element, Dynamic Type scales to XXL without truncation, reduced-motion disables call PiP and onboarding animations, AA contrast on every scheme.
20. ✅ **Force-upgrade gate verified** in staging — bumping `min_client_version` past current shows the blocking modal on next foreground.
21. ✅ **Offline queue verified**: airplane-mode the device, send 3 messages, take it back online, all 3 send in order with no duplicates.
22. ✅ **Sentry receives a test crash** from each environment (development, preview, production-rc) before submission.
23. ✅ **App icon switches** to the correct variant on iOS when the user changes scheme. Splash matches the persisted scheme on next launch.
24. ✅ **Haptic feedback fires** on every UX moment listed in §4.11 — confirmed by the operator on a haptic-capable iPhone.
25. ✅ **App store metadata in `apps/mobile/store/`** is complete: descriptions, screenshots per scheme, privacy policy URL, support URL.
26. ✅ **Admin tab visibility verified**: an account with `role === 'admin'` sees the fourth Admin tab; a regular user does not. Direct deep-link to `/admin/*` as a regular user redirects to the conversations tab. Impersonation banner appears + clears correctly.

---

## 18. Notes for the operator

- This spec is locked the same way `WAKEUP.md` is. If you (the operator) want to change a stack choice, edit this file and commit — don't paper over it with code.
- The Maestro flows are the testing strategy. Don't skip writing them; the operator-review loop depends on them existing.
- The cookie-jar persistence on RN is the single most likely source of "auth doesn't work" pain on day one. Verify it before building the rest of the auth flow.
- The 10 themes are decorative but the picker is part of the "minimalist + clean" feel. The icons matter. Don't substitute generic moon/sun emojis — use the named lucide icons.
- The widget phase (§16 Phase 12) is the most platform-divergent. Schedule extra time and treat it as v1's hardest piece.
- The §10.3 backend lone-kick semantics rely on the client cleanly handling `room.ended`. Test the lone-kick scenario explicitly: join a call, leave the second user, wait 5 minutes, expect a clean disconnect with a "Call ended — everyone else left" toast.
- **CallKit + PushKit (§8.6) is the single highest-risk piece for App Store review.** Apple rejects VoIP apps that don't surface incoming calls through CallKit. Don't punt this. The Phase 9.10 + 9.11 milestones are not optional.
- **The privacy manifest (§10.5) re-audits on every dependency add.** Adding a new SDK without updating `PrivacyInfo.xcprivacy` will silently fail submission with a misleading error. Make this the last commit in any dependency-bump PR.
- **Account deletion (§10.5) ships before submission.** Apps without it are rejected at review.
- **The 11 app icon variants are real assets, not placeholders.** The operator must produce 1024×1024 PNGs per scheme (10 + default) before Phase 11.6 closes. Same for splash images.
- **Backend follow-ups this spec assumed (now closed).** Audited 2026-05-03 against the backend, reaudited 2026-05-04 after the cleanup PRs landed. All eight endpoints the mobile spec depended on are shipped: (1) `/v1/healthz` returning JSON with `min_client_version` (#104); (2) `POST /v1/devices/voip` for iOS PushKit (#105); (3) `GET /v1/blocks` (#104); (4) `GET /v1/devices` (#104); (5) `GET /v1/search` unified search across users / conversations / messages (#107); (6) `/.well-known/apple-app-site-association` + `assetlinks.json` (#104, gated on `IOS_APP_ID` / `ANDROID_PACKAGE` env keys); (7) `X-Unread-Total` response header on `GET /v1/auth/me` (#104); (8) WS `heartbeat` ack carrying `unread_total` (#104). The only remaining backend-side gap the mobile flow touches is the seeded admin account in test fixtures for the Maestro admin flows (§10.4) — tracked separately, not gating the v1 mobile cut.
