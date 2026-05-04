// Package domain holds the aggregate models repositories return and services
// consume. Per WAKEUP.md §4.11, this package may import only stdlib +
// `github.com/google/uuid`. It must NOT import internal/repository,
// internal/service, internal/handler, or any other internal package.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// User mirrors the `users` row created by migration 0001_init. It is the
// type returned by repository/user.Get* and consumed by service/user; the
// handler layer converts it to a UserResponse / MeResponse DTO at the wire
// boundary so PasswordHash never reaches a client.
type User struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	Email        string
	PasswordHash string // never marshaled to JSON — handler DTOs strip it (§4.10)
	AvatarURL    *string
	Bio          *string // ≤ 280 chars; nil = no bio set
	StatusEmoji  *string // ≤ 8 chars (one emoji + variant selector); nil = none
	ColorScheme  string  // "light" | "dark" | "system"
	Role         string  // "user" | "admin"
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time // soft delete; see §4.6 soft-delete rules
}
