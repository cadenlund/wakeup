package testutil

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/livekit/protocol/auth"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// LiveKit dev-mode keys. The livekit-server `--dev` flag pre-loads
// these credentials into the keyfile, so we hard-code them here so
// the SDK + the webhook receiver can sign matching tokens without
// reading the container's config file.
//
// These are NOT secrets — they're documented in the LiveKit repo's
// dev mode docs. Production deploys generate fresh keys per env.
const (
	LiveKitDevAPIKey    = "devkey"
	LiveKitDevAPISecret = "secret"
)

// LiveKitTestEnv is the per-binary handle for the LiveKit testcontainer.
// KeyProvider wraps the dev keys so webhook handler tests in §12.8.3
// can synthesize a body, sign it via auth.NewAccessToken, and pass
// the same keys back through webhook.Receive on the handler side
// without spinning up a real LiveKit.
type LiveKitTestEnv struct {
	URL         string // ws://host:port
	APIKey      string
	APISecret   string
	KeyProvider auth.KeyProvider
}

var (
	livekitOnce sync.Once
	livekitEnv  LiveKitTestEnv
	livekitErr  error
)

// StartLiveKit ensures a livekit-server container is running and
// returns a LiveKitTestEnv (URL, dev API key/secret, and an
// auth.KeyProvider seeded with the same keys). One real LiveKit per
// test binary; reused via sync.Once because boot is ~2s.
//
// The webhookURL parameter is reserved for future Phase 10.5 e2e
// wiring (where real LiveKit fires webhooks at the harness's HTTP
// server). For 10.1 we accept the parameter but don't yet plumb it
// into the container — §12.8.3 webhook tests synthesize webhook
// bodies and sign them with auth.NewAccessToken, then verify them on
// the handler side via webhook.Receive(req, env.KeyProvider). No live
// container is needed for those tests. (CodeRabbit PR #55.)
func StartLiveKit(t *testing.T, _ string) LiveKitTestEnv {
	t.Helper()
	livekitOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
		defer cancel()
		req := testcontainers.ContainerRequest{
			Image:        "livekit/livekit-server:v1.7.2",
			ExposedPorts: []string{"7880/tcp"},
			Cmd:          []string{"--dev", "--bind", "0.0.0.0"},
			WaitingFor: wait.ForLog("starting LiveKit server").
				WithStartupTimeout(containerStartTimeout),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			livekitErr = fmt.Errorf("StartLiveKit: run container: %w", err)
			return
		}
		// Container leak guard: if Host/MappedPort fails after the
		// container is up, terminate so we don't strand it on the
		// host. Cleared on success so the long-running container
		// survives for the rest of the test binary. (CodeRabbit PR #55.)
		started := false
		defer func() {
			if !started {
				_ = c.Terminate(ctx)
			}
		}()
		host, err := c.Host(ctx)
		if err != nil {
			livekitErr = fmt.Errorf("StartLiveKit: host: %w", err)
			return
		}
		port, err := c.MappedPort(ctx, "7880")
		if err != nil {
			livekitErr = fmt.Errorf("StartLiveKit: port: %w", err)
			return
		}
		started = true
		// KeyProvider feeds webhook.Receive's signature check —
		// constructed with the same dev keys the container ships
		// with so a synthesized webhook signed via
		// auth.NewAccessToken passes verification on the handler
		// side. Webhook tests don't need the live container; they
		// just need matching keys.
		livekitEnv = LiveKitTestEnv{
			URL:         fmt.Sprintf("ws://%s:%s", host, port.Port()),
			APIKey:      LiveKitDevAPIKey,
			APISecret:   LiveKitDevAPISecret,
			KeyProvider: auth.NewSimpleKeyProvider(LiveKitDevAPIKey, LiveKitDevAPISecret),
		}
	})
	if livekitErr != nil {
		t.Fatalf("%v (is Docker running?)", livekitErr)
	}
	return livekitEnv
}

// LiveKitClient connects to env's LiveKit as the given identity using
// the supplied token. Returns the connected *lksdk.Room — caller is
// responsible for calling Disconnect (or letting test cleanup do it).
func (h *Harness) LiveKitClient(t *testing.T, env LiveKitTestEnv, identity, token string) *lksdk.Room {
	t.Helper()
	cb := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{},
	}
	room, err := lksdk.ConnectToRoomWithToken(env.URL, token, cb)
	if err != nil {
		t.Fatalf("LiveKitClient: connect %s: %v", identity, err)
	}
	t.Cleanup(room.Disconnect)
	return room
}

// IssueLiveKitToken builds a JWT for the given roomName + identity
// using env's dev keys. Convenience wrapper for tests that need to
// hand a participant a valid join token without reaching into the
// production room service.
//
// The grant matches the §10 production shape: roomJoin=true with
// publish + subscribe permissions. Tests asserting precise JWT
// claims should construct their own at.AccessToken instance for
// stricter control.
func IssueLiveKitToken(t *testing.T, env LiveKitTestEnv, roomName, identity string) string {
	t.Helper()
	at := auth.NewAccessToken(env.APIKey, env.APISecret).
		SetIdentity(identity).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:     true,
			Room:         roomName,
			CanPublish:   ptr(true),
			CanSubscribe: ptr(true),
		})
	tok, err := at.ToJWT()
	if err != nil {
		t.Fatalf("IssueLiveKitToken: %v", err)
	}
	return tok
}

func ptr[T any](v T) *T { return &v }
