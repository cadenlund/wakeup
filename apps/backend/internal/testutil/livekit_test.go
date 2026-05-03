package testutil_test

import (
	"testing"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// LiveKit container boots and the SDK can dial it with a valid
// token issued for the dev keys.
func TestStartLiveKit_BootAndConnect(t *testing.T) {
	t.Parallel()
	env := testutil.StartLiveKit(t, "")
	if env.URL == "" {
		t.Fatal("URL is empty")
	}
	if env.APIKey == "" || env.APISecret == "" {
		t.Fatal("dev keys missing")
	}
	if env.KeyProvider == nil {
		t.Fatal("KeyProvider missing")
	}

	tok := testutil.IssueLiveKitToken(t, env, "smoke-room", "smoke-user")
	if tok == "" {
		t.Fatal("IssueLiveKitToken returned empty token")
	}

	// Connect via the SDK; t.Cleanup-bound disconnect lives inside
	// the helper.
	cb := &lksdk.RoomCallback{}
	room, err := lksdk.ConnectToRoomWithToken(env.URL, tok, cb)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(room.Disconnect)
	if room.Name() != "smoke-room" {
		t.Errorf("Name = %q, want smoke-room", room.Name())
	}
}

// A token built with the wrong secret must be rejected by the
// container. Verifies our dev-keys constants actually match the
// container's --dev mode keys (catches a future LiveKit version that
// changes them).
func TestStartLiveKit_RejectsBadToken(t *testing.T) {
	t.Parallel()
	env := testutil.StartLiveKit(t, "")
	cb := &lksdk.RoomCallback{}
	room, err := lksdk.ConnectToRoom(env.URL, lksdk.ConnectInfo{
		APIKey:              env.APIKey,
		APISecret:           "wrong-secret",
		RoomName:            "smoke-room",
		ParticipantIdentity: "nope",
	}, cb)
	if err == nil {
		t.Cleanup(room.Disconnect)
		t.Fatal("expected dial with bad-secret token to fail")
	}
}

// LiveKitClient connects via the harness so callers don't have to
// manage Disconnect manually.
func TestHarness_LiveKitClient(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	env := testutil.StartLiveKit(t, "")
	tok := testutil.IssueLiveKitToken(t, env, "harness-room", "harness-user")
	room := h.LiveKitClient(t, env, "harness-user", tok)
	if room.Name() != "harness-room" {
		t.Errorf("Name = %q, want harness-room", room.Name())
	}
}

// Reuse: two callers receive the same env (containers.go's sync.Once
// pattern). Mostly defensive — sweet/sour breakage would surface as
// a sudden test slowdown.
func TestStartLiveKit_ReusesContainer(t *testing.T) {
	t.Parallel()
	a := testutil.StartLiveKit(t, "")
	b := testutil.StartLiveKit(t, "")
	if a.URL != b.URL {
		t.Errorf("URLs differ: %s vs %s — sync.Once not reusing container", a.URL, b.URL)
	}
}
