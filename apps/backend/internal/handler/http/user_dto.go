package httpapi

import (
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// UserListResponse is the §6.4 paginated envelope for GET /v1/users.
type UserListResponse struct {
	Data       []UserResponse `json:"data"`
	NextCursor *string        `json:"next_cursor"        example:"eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0="`
	HasMore    bool           `json:"has_more"           example:"true"`
}

// UpdateMeRequest is the body for PATCH /v1/users/me. All fields optional;
// nil-means-unchanged matches the service's COALESCE pattern.
//
// Bio + StatusEmoji: send `""` (empty string) to clear; the service treats
// "" the same as "no bio set" — UI hides empty values rather than rendering
// them. Send the field as null in JSON (or omit it) to leave unchanged.
type UpdateMeRequest struct {
	DisplayName *string `json:"display_name,omitempty" validate:"omitempty,min=1,max=64"             example:"Caden Lund"`
	AvatarURL   *string `json:"avatar_url,omitempty"   validate:"omitempty,url,max=2048"             example:"https://wakeup.app/avatars/caden.png"`
	ColorScheme *string `json:"color_scheme,omitempty" validate:"omitempty,oneof=light dark system"  example:"dark"`
	Bio         *string `json:"bio,omitempty"          validate:"omitempty,max=280"                  example:"Building things at night."`
	StatusEmoji *string `json:"status_emoji,omitempty" validate:"omitempty,max=8"                    example:"🛌"`
}

// AvatarUploadResponse is returned by POST /v1/users/me/avatar after a
// successful upload.
type AvatarUploadResponse struct {
	User MeResponse `json:"user"`
}

// NotificationPreferencesResponse is the body of GET /v1/users/me/notifications
// and the success body of PATCH /v1/users/me/notifications. Despite the
// "notifications" name the row also stores the §4.5 theme pick, so the
// response includes those columns too — one round-trip on app start
// gives the client everything it needs to paint and gate notifications.
type NotificationPreferencesResponse struct {
	DirectMessages      bool   `json:"direct_messages"       example:"true"`
	GroupMessages       bool   `json:"group_messages"        example:"true"`
	FriendRequests      bool   `json:"friend_requests"       example:"true"`
	Calls               bool   `json:"calls"                 example:"true"`
	ThemeScheme         string `json:"theme_scheme"          example:"system" enums:"system,sunrise,daylight,noon,golden,meadow,dusk,twilight,aurora,midnight,rem"`
	ThemeModePreference string `json:"theme_mode_preference" example:"system" enums:"system,light,dark"`
}

// UpdateNotificationPreferencesRequest is the body for
// PATCH /v1/users/me/notifications. Each pointer uses nil-means-unchanged.
// `theme_scheme` and `theme_mode_preference` accept the same enum
// values as the response — service validates and returns 400 with a
// FieldError on bad values before the DB CHECK constraint fires.
type UpdateNotificationPreferencesRequest struct {
	DirectMessages      *bool   `json:"direct_messages,omitempty"       example:"false"`
	GroupMessages       *bool   `json:"group_messages,omitempty"        example:"true"`
	FriendRequests      *bool   `json:"friend_requests,omitempty"       example:"true"`
	Calls               *bool   `json:"calls,omitempty"                 example:"false"`
	ThemeScheme         *string `json:"theme_scheme,omitempty"          example:"midnight" enums:"system,sunrise,daylight,noon,golden,meadow,dusk,twilight,aurora,midnight,rem"`
	ThemeModePreference *string `json:"theme_mode_preference,omitempty" example:"dark"     enums:"system,light,dark"`
}

// toUserResponse renders the public view of a domain.User. Soft-deleted
// users collapse to the §4.6 placeholder so callers can't enumerate
// real usernames via deleted accounts.
func toUserResponse(u domain.User) UserResponse {
	if u.DeletedAt != nil {
		return UserResponse{
			ID: u.ID, Username: "deleted-user", DisplayName: "Deleted User",
			AvatarURL: nil, Bio: nil, StatusEmoji: nil, CreatedAt: u.CreatedAt,
		}
	}
	return UserResponse{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
		AvatarURL: presignAvatarKey(u.AvatarURL), Bio: u.Bio, StatusEmoji: u.StatusEmoji,
		CreatedAt: u.CreatedAt,
	}
}

// toUserListResponse builds the paginated envelope from a service search result.
func toUserListResponse(users []domain.User, next *string, hasMore bool) UserListResponse {
	out := UserListResponse{
		Data:       make([]UserResponse, len(users)),
		NextCursor: next,
		HasMore:    hasMore,
	}
	for i, u := range users {
		out.Data[i] = toUserResponse(u)
	}
	return out
}

// toNotificationPreferencesResponse renders the §6.2 GET/PATCH body.
func toNotificationPreferencesResponse(p domain.NotificationPreference) NotificationPreferencesResponse {
	return NotificationPreferencesResponse{
		DirectMessages:      p.DirectMessages,
		GroupMessages:       p.GroupMessages,
		FriendRequests:      p.FriendRequests,
		Calls:               p.Calls,
		ThemeScheme:         p.ThemeScheme,
		ThemeModePreference: p.ThemeModePreference,
	}
}
