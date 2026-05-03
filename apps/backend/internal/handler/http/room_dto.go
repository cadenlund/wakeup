package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/service/room"
)

// JoinRoomRequest is the body of POST /v1/conversations/{id}/room/join.
// `video` is a UI hint baked into the JWT metadata so other
// participants can render the camera-on indicator without a server
// round-trip; it does NOT change the token's publish permissions
// (§10.3 / §12.8.1 token_video_flag_propagated).
type JoinRoomRequest struct {
	Video bool `json:"video" example:"false"`
}

// JoinRoomResponse mirrors §6.2 / §10.3 join contract.
type JoinRoomResponse struct {
	RoomID       string    `json:"room_id"        example:"conv:0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	LiveKitURL   string    `json:"livekit_url"    example:"ws://localhost:7880"`
	LiveKitToken string    `json:"livekit_token"  example:"eyJhbGciOi..."`
	ExpiresAt    time.Time `json:"expires_at"     example:"2026-05-02T10:52:55.412Z"`
	Video        bool      `json:"video"          example:"false"`
}

// RoomParticipantRow is one entry in the GET /room participants list.
type RoomParticipantRow struct {
	UserID   uuid.UUID `json:"user_id"   example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	JoinedAt time.Time `json:"joined_at" example:"2026-05-02T10:42:55.412Z"`
	Video    bool      `json:"video"     example:"false"`
}

// RoomStateResponse is the body of GET /v1/conversations/{id}/room.
type RoomStateResponse struct {
	Participants []RoomParticipantRow `json:"participants"`
	StartedAt    *time.Time           `json:"started_at"   example:"2026-05-02T10:42:55.412Z"`
}

// toJoinRoomResponse converts a room.JoinResult into the wire shape.
func toJoinRoomResponse(r room.JoinResult) JoinRoomResponse {
	return JoinRoomResponse{
		RoomID:       r.RoomID,
		LiveKitURL:   r.LiveKitURL,
		LiveKitToken: r.LiveKitToken,
		ExpiresAt:    r.ExpiresAt,
		Video:        r.Video,
	}
}

// toRoomStateResponse renders a room.State into the §6.2 wire shape.
func toRoomStateResponse(s room.State) RoomStateResponse {
	rows := make([]RoomParticipantRow, 0, len(s.Participants))
	for _, p := range s.Participants {
		rows = append(rows, RoomParticipantRow{
			UserID: p.UserID, JoinedAt: p.JoinedAt, Video: p.Video,
		})
	}
	return RoomStateResponse{Participants: rows, StartedAt: s.StartedAt}
}
