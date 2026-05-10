// Package integration_test hosts the §12.8.4 end-to-end flows the
// regular package-scoped tests can't cover. The proof-of-life test
// here is the gate the spec calls out as "the backend works perfectly
// before we start the frontend."
//
// Scope today: real LiveKit container + real backend HTTP API + real
// lksdk client → assert the JWT we issue is accepted by LiveKit and
// the participant lands in the room. The full webhook-fan-out path
// (real LiveKit → backend webhook → Redis state → WS broadcast)
// requires a per-binary webhook routing server that the harness
// doesn't yet provide; that's deferred to a follow-up. The webhook
// dispatch logic is already covered by §12.8.3 unit tests using the
// synthesizer pattern (livekit_webhook_handler_test.go).
package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// joinRoom POSTs /v1/conversations/{id}/room/join and returns the
// server-issued LiveKit URL + token. Asserts a 200 and the basic
// response shape.
func joinRoom(t *testing.T, h *testutil.Harness, c *http.Client, convID string, video bool) (string, string) {
	t.Helper()
	body := map[string]any{"video": video}
	raw, _ := json.Marshal(body)
	resp, err := c.Post(h.Server.URL+"/v1/conversations/"+convID+"/room/join",
		"application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("POST /room/join: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("/room/join status=%d body=%s", resp.StatusCode, b)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	url, _ := got["livekit_url"].(string)
	tok, _ := got["livekit_token"].(string)
	if url == "" || tok == "" {
		t.Fatalf("missing url/token in response: %#v", got)
	}
	return url, tok
}

// TestLiveKit_EndToEnd is the §12.8.4 proof-of-life integration test.
// Two backend users join the same conversation room via the real HTTP
// API; both connect to the live LiveKit container with the issued
// tokens; the test asserts both participants are visible to each
// other through LiveKit's own state, and that LiveKit accepts the
// JWTs without modification.
//
// The webhook → backend → WS path is covered by the §12.8.3 handler
// tests (which synthesize valid signatures). Wiring the live
// container's webhook URL at the harness's TLS server is on the
// backlog — a per-test LiveKit container with a per-test config
// file is the simplest path forward and is left for a follow-up.
func TestLiveKit_EndToEnd(t *testing.T) {
	// ICE candidate negotiation goes out to public STUN servers and is
	// flaky under heavy parallel test load (the lefthook full-suite run
	// reliably reproduces it). Skip under `go test -short` so the local
	// pre-commit hook stays deterministic; CI runs without `-short` and
	// gets the full coverage.
	if testing.Short() {
		t.Skip("livekit e2e: skipping under -short (run without -short in CI)")
	}
	t.Parallel()
	h := testutil.New(t)
	env := testutil.StartLiveKit(t, "")

	// Both backend users authenticate AND register their LiveKit URL
	// override. The harness's RoomSvc is configured with a dummy
	// LiveKit URL during construction; for this test we want the
	// service to issue tokens that point at the real container, so
	// we go around the harness's RoomSvc and call /v1/room/join via
	// HTTP (the response includes whatever URL the service was
	// configured with — a follow-up should take a livekit_url override
	// at harness construction time).
	//
	// For the live-LiveKit assertion below we ignore the URL the
	// HTTP API returned and dial env.URL directly, using the token
	// from the API. The token is signed with dev keys that match
	// the container's --dev mode.
	aliceClient, alice := h.AuthClient(t)
	bobClient, bob := h.AuthClient(t)
	h.MakeFriendship(t, alice, bob)
	res, err := h.ConvSvc.Create(context.Background(), conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: alice.ID, MemberIDs: []uuid.UUID{bob.ID},
	})
	if err != nil {
		t.Fatalf("Create direct: %v", err)
	}
	convID := res.Conversation.ID.String()

	_, aliceTok := joinRoom(t, h, aliceClient, convID, false)
	_, bobTok := joinRoom(t, h, bobClient, convID, true)

	// Connect both clients to the real LiveKit. The container is the
	// proof point: if the JWT we issued isn't shaped exactly right,
	// LiveKit refuses the connection.
	cb := &lksdk.RoomCallback{}
	aliceRoom, err := lksdk.ConnectToRoomWithToken(env.URL, aliceTok, cb)
	if err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	t.Cleanup(aliceRoom.Disconnect)
	bobRoom, err := lksdk.ConnectToRoomWithToken(env.URL, bobTok, cb)
	if err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	t.Cleanup(bobRoom.Disconnect)

	// Wait for the LiveKit-internal participant list to settle.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(aliceRoom.GetRemoteParticipants()) >= 1 &&
			len(bobRoom.GetRemoteParticipants()) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := len(aliceRoom.GetRemoteParticipants()); got != 1 {
		t.Errorf("alice sees %d remote participants, want 1", got)
	}
	if got := len(bobRoom.GetRemoteParticipants()); got != 1 {
		t.Errorf("bob sees %d remote participants, want 1", got)
	}

	// Identity check: alice's view of bob == "user:<bob.ID>".
	for _, rp := range aliceRoom.GetRemoteParticipants() {
		want := "user:" + bob.ID.String()
		if rp.Identity() != want {
			t.Errorf("alice remote identity = %q, want %q", rp.Identity(), want)
		}
	}

}
