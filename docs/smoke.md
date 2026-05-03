# Manual smoke test — Wakeup backend

Per WAKEUP.md §16 milestone 13.11. This script walks the §6.1 happy
path using the Swagger UI at `http://localhost:8080/v1/docs/` plus a
LiveKit web client (https://meet.livekit.io pointed at the local
LiveKit testcontainer with the JWT issued by `/v1/conversations/{id}/room/join`).

The goal is to flush out anything the automated suites can't reach in
autonomous CI: real browser cookies / WS upgrades / multipart uploads
and a real LiveKit client streaming audio + video.

## Prerequisites

```bash
just dev           # brings up postgres, redis, minio, livekit
just migrate-up    # apply migrations
just dev-server    # starts the backend on :8080
```

Open two browser windows (call them **A** and **B**). Each will own its
own session cookie via Swagger UI.

## 1. Register two users

In **A**'s Swagger UI, run `POST /v1/auth/register` with:

```json
{
  "username": "alice",
  "email": "alice@x.test",
  "display_name": "Alice",
  "password": "Password123!"
}
```

Expect `201 Created`. The session cookie should be set by the response.
Confirm with `GET /v1/auth/me` → 200 with `username: "alice"`.

Repeat in **B** with `bob` / `bob@x.test`.

## 2. Become friends

In **A**: `POST /v1/friends/requests` with `{"username": "bob"}`.
Expect `201 Created` with the friendship row.

In **B**: `GET /v1/friends/requests` → expect one incoming row.
Then `POST /v1/friends/requests/{id}/accept` with the friendship id.
Expect `200 OK` and `status: "accepted"`.

In **A**: `GET /v1/friends` → expect Bob in the list.

## 3. Create a group conversation

In **A**: `POST /v1/conversations` with:

```json
{
  "type": "group",
  "name": "Smoke Group",
  "member_ids": ["<bob-uuid-from-step-2>"]
}
```

Expect `201 Created` with `id` and `members`. Note the conversation id.

In **B**: `GET /v1/conversations` → the new group should appear.

## 4. Send messages — and exercise idempotency

In **A**: `POST /v1/conversations/{id}/messages` with `{"body": "first message"}`.
Expect `201 Created` with the message id; the `Idempotent-Replay` header
must be absent when no `Idempotency-Key` was sent.

Now generate a UUID v7 client-side (any UUID works) and send the SAME
body twice with the same `Idempotency-Key` header:

```http
POST /v1/conversations/{id}/messages
Idempotency-Key: <client-generated-uuid-v7>
Content-Type: application/json

{"body": "retried message"}
```

First call: `201 Created`, `Idempotent-Replay: false`. Second call (same
key + same body): `201 Created` with the EXACT same JSON body and
`Idempotent-Replay: true`. The handler must NOT have run a second time —
verify by checking the message list (`GET /v1/conversations/{id}/messages`):
exactly two new messages, not three.

Now retry the same key with a DIFFERENT body:

```json
{"body": "different body, same key"}
```

Expect `422 Unprocessable Entity` with `"code": "IDEMPOTENCY_KEY_REUSED"`.

## 5. Upload an attachment

In **A**: `POST /v1/attachments` with a multipart form containing
`file=<some-image>`. Expect `201 Created` with the attachment id and a
presigned `url`.

Then `POST /v1/conversations/{id}/messages` with:

```json
{
  "body": "look at this",
  "attachment_ids": ["<attachment-id>"]
}
```

Expect `201`. In **B**, hit the same `GET /v1/conversations/{id}/messages`
and confirm the message includes the attachment with the presigned URL.
Click the URL — the image should load.

## 6. Presence

In a third browser tab (or a curl loop), open a WebSocket to
`/v1/ws` carrying **A**'s session cookie. The server emits a
`presence.update` for **A** to all of A's friends.

In **B**'s Swagger UI: `GET /v1/presence/friends`. Alice should appear
as `online`. Close A's WS connection. Within ~5 minutes (the §9.2 decay
cutoff) Alice should appear as `away`, then `offline` after another
hour.

To force the transition immediately, in **A**:
`POST /v1/presence/status` with `{"status": "sleeping"}`. **B**'s
re-fetched `/v1/presence/friends` should show Alice as `sleeping`.

## 7. Voice + video room

In **A**: `POST /v1/conversations/{id}/room/join` with `{"video": false}`.
Response: `room_id`, `livekit_url`, `livekit_token` (10-min TTL).

Open https://meet.livekit.io. Set:
- LiveKit URL: `ws://localhost:7880` (the testcontainer's exposed URL)
- Token: paste `livekit_token` from the response

Connect. Audio should publish.

In **B**: same `POST /v1/conversations/{id}/room/join` and connect the
LiveKit client the same way. Confirm:
- Both parties hear each other's audio
- `GET /v1/conversations/{id}/room` lists both `participants` with
  `joined_at` timestamps
- `room.participant_joined` was broadcast on the WS for any other
  conversation member listening

In **B**: toggle the camera in the LiveKit UI. Confirm:
- **A** sees B's video feed
- The backend emitted a `room.video_changed` WS event
- `GET .../room` reflects `video: true` for B

## 8. Cleanup

In each session: `POST /v1/auth/logout` (idempotent — even with no live
session the response is 204). Stop the LiveKit clients.

## What success looks like

- Every step's HTTP status matches the §6.1 contract
- Idempotency replay returns byte-identical bodies + `Idempotent-Replay: true`
- Idempotency-Key reuse with a different body returns 422
- Real browser cookies round-trip (Secure + HttpOnly + SameSite=Lax)
- Multipart upload places a real object in MinIO, presigned URL renders
- Presence transitions fire WS events and reflect in the friends feed
- LiveKit JWT is accepted by a third-party client; both audio and video
  publish

If any step diverges from the above, the bug is reproducible from this
script — copy/paste the failing curl into an issue.
