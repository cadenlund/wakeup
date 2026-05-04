# Maestro flows

End-to-end flows for the Wakeup mobile app. Authoritative spec lives in [WAKEUPEXPO.md §12.7](../../../docs/WAKEUPEXPO.md) (flow catalog) and §12.8 (assertion conventions).

## Layout

```text
.maestro/
├── flows/
│   ├── _shared/                  # reusable sub-flows (login, register, seeders)
│   │   ├── login.yaml
│   │   ├── register.yaml
│   │   ├── seed-friend.yaml
│   │   └── seed-conversation.yaml
│   ├── auth-login.yaml           # one file per §5.1 screen
│   ├── conversation-thread.yaml
│   └── …
└── screenshots/                  # committed; CI diffs new run vs committed
    └── <flow-name>/
```

## Running

```sh
# every flow
bunx maestro test .maestro/

# one flow against the iOS Simulator
bunx maestro test .maestro/flows/auth-login.yaml

# capture fresh screenshots into the .maestro/screenshots/ tree
bunx maestro test .maestro/ --include-tags screenshot
```

The Maestro MCP (configured in §14.1) drives this same set from inside the implementer's Claude Code session — same flows, same assertions, just hands-free during the per-milestone loop.

## The operator-review loop

Maestro screenshots are the implementer's sanity check, **not** the gate. Per [WAKEUPEXPO.md §12.4](../../../docs/WAKEUPEXPO.md), every screen-bearing milestone ends with:

1. Implementer runs flows + captures screenshots.
2. Implementer runs `bunx expo start --tunnel` and posts the QR.
3. Operator scans the QR with Expo Go on their phone.
4. Operator approves or hands back corrections.
5. Implementer commits.

Skipping step 3 is a process violation.

## Conventions

See WAKEUPEXPO.md §12.8 for the full list. Highlights:

- `assertVisible:` uses literal user-visible text, not test ids.
- `tapOn:` targets `accessibilityLabel` so the labels are real (helps the §10 accessibility audit too).
- `clearState: true` at the top of every screen-level flow; sub-flows never clear state.
- Every flow ends with at least one `assertVisible:` so silent failures fail CI.
- Use `extendedWaitUntil:` (10s default) for network-dependent assertions.

## Adding a new flow

1. The flow file's name matches the milestone in §16 (e.g., `friends-add.yaml` for milestone 4.3).
2. Reuse `flows/_shared/login.yaml` via `runFlow:` rather than re-typing credentials.
3. Tap by `accessibilityLabel`; assert by literal visible text.
4. Add a screenshot at the final assertion point.
5. Run locally → commit screenshot diff alongside the flow.
