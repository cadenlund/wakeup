package domain

import (
	"time"

	"github.com/google/uuid"
)

// DevicePlatform mirrors the migration 0009 CHECK constraint.
type DevicePlatform string

// Platform constants for the §6.1 device-token endpoints.
const (
	DeviceIOS     DevicePlatform = "ios"
	DeviceAndroid DevicePlatform = "android"
)

// IsValid reports whether s is one of the known platforms.
func (s DevicePlatform) IsValid() bool {
	switch s {
	case DeviceIOS, DeviceAndroid:
		return true
	}
	return false
}

// DeviceToken mirrors a row in device_tokens (migration 0009). The
// (user_id, expo_token) UNIQUE index means a re-register with the
// same token is an UPDATE-by-pair rather than a duplicate row.
type DeviceToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	ExpoToken  string
	Platform   DevicePlatform
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// VoIPToken mirrors a row in voip_tokens (migration 0009). iOS-only:
// PushKit tokens wake the app from a fully-killed state and are
// delivered via Apple's PushKit transport (separate from APNS / Expo).
// The mobile spec requires this for the §8.6 CallKit incoming-call ring.
type VoIPToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	VoIPToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
}
