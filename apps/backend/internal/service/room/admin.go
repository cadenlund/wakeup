package room

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// LiveKitAdmin is the slice of LiveKit's RoomServiceClient the §10.3
// lone-kick sweeper needs. The interface lets tests substitute a fake
// without spinning up a real LiveKit container — the kick code path
// is just one method call, so the seam is small.
type LiveKitAdmin interface {
	// RemoveParticipant kicks identity from room via the LiveKit admin
	// RPC. Idempotent: a second call after the participant has already
	// disconnected returns an error from LiveKit, which the caller
	// logs but doesn't escalate (the participant is gone either way).
	RemoveParticipant(ctx context.Context, room, identity string) error
}

// NewLiveKitAdmin wraps an *lksdk.RoomServiceClient as a LiveKitAdmin.
// cmd/server constructs one with the same (apiKey, apiSecret, url)
// triple already in env. Returns an error on missing inputs so a
// misconfigured deploy fails at boot rather than at first kick.
func NewLiveKitAdmin(url, apiKey, apiSecret string) (LiveKitAdmin, error) {
	if url == "" {
		return nil, fmt.Errorf("room: NewLiveKitAdmin: url is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("room: NewLiveKitAdmin: apiKey is required")
	}
	if apiSecret == "" {
		return nil, fmt.Errorf("room: NewLiveKitAdmin: apiSecret is required")
	}
	// LiveKit's admin RPC speaks HTTP, not WebSocket. The server-sdk
	// already does the protocol swap internally — pass the same URL the
	// frontend uses; the SDK derives the HTTP origin.
	return &liveKitAdmin{
		client: lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
	}, nil
}

type liveKitAdmin struct {
	client *lksdk.RoomServiceClient
}

func (c *liveKitAdmin) RemoveParticipant(ctx context.Context, room, identity string) error {
	_, err := c.client.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     room,
		Identity: identity,
	})
	if err != nil {
		return fmt.Errorf("room: livekit RemoveParticipant: %w", err)
	}
	return nil
}
