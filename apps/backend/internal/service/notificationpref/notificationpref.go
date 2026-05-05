// Package notificationpref is the service layer for per-user push-
// notification toggles AND theme preferences. Composes the
// notificationpref repository (§3.4) behind a thin apierror-typed
// surface.
//
// Methods:
//   - GetForUser     : returns the row, auto-creating a defaults row
//     (all booleans true, theme = 'system'/'system') on first call.
//   - UpdateForUser  : patches any subset of fields. Ensures the row
//     exists first so a brand-new user's first patch succeeds without a
//     separate Get call. Validates theme enum values before delegating
//     to the repo so the DB CHECK constraint never trips.
//   - ShouldNotify   : per-category bool check used by §11 trigger sites.
//     Defaults to true if no row exists (a fresh user gets all-true via
//     the schema defaults). Fails open on DB errors — better to
//     over-notify than miss.
package notificationpref

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
)

// Service is the notification-preferences service.
type Service struct {
	prefs *repo.Queries
}

// Config builds the service.
type Config struct {
	Prefs *repo.Queries
}

// New constructs the service. Returns an error if any dependency is missing.
func New(cfg Config) (*Service, error) {
	if cfg.Prefs == nil {
		return nil, errors.New("notificationpref: Config.Prefs is required")
	}
	return &Service{prefs: cfg.Prefs}, nil
}

// Category names a single push-notification toggle column. The string
// values match the category names used in WAKEUP.md §11 trigger sites
// (and may surface in logs / metrics).
type Category string

// Category constants for ShouldNotify. Adding a new column requires
// adding a constant and the corresponding case in ShouldNotify.
const (
	CategoryDirectMessages Category = "direct_messages"
	CategoryGroupMessages  Category = "group_messages"
	CategoryFriendRequests Category = "friend_requests"
	CategoryCalls          Category = "calls"
)

// UpdateParams is the input to UpdateForUser. Each pointer field uses
// nil-means-unchanged semantics — matches the repo's COALESCE pattern
// and lets handlers forward partial PATCH bodies straight through.
type UpdateParams struct {
	UserID              uuid.UUID
	DirectMessages      *bool
	GroupMessages       *bool
	FriendRequests      *bool
	Calls               *bool
	ThemeScheme         *string
	ThemeModePreference *string
}

// validThemeSchemes mirrors the CHECK constraint in migration 0012 for
// `theme_scheme`. Centralized so the service rejects bad values with a
// clean apierror.Validation rather than letting them hit Postgres and
// surface as a generic 500.
var validThemeSchemes = map[string]struct{}{
	"system": {}, "sunrise": {}, "daylight": {}, "noon": {},
	"golden": {}, "meadow": {}, "dusk": {}, "twilight": {},
	"aurora": {}, "midnight": {}, "rem": {},
}

// validThemeModes mirrors the CHECK constraint in migration 0012 for
// `theme_mode_preference`. "system" follows OS Appearance; "light"/
// "dark" override it.
var validThemeModes = map[string]struct{}{
	"system": {}, "light": {}, "dark": {},
}

// GetForUser returns the user's preference row, auto-creating one with
// the schema defaults (all booleans true) on first call.
func (s *Service) GetForUser(ctx context.Context, userID uuid.UUID) (domain.NotificationPreference, error) {
	pref, err := s.prefs.GetOrCreate(ctx, userID)
	if err != nil {
		return domain.NotificationPreference{}, apierror.Internal("get notification preferences").WithCause(err)
	}
	return pref, nil
}

// ShouldNotify returns whether the user has the given category enabled.
// Defaults to true (notify) when:
//   - the user has no preferences row yet (matches schema-default
//     all-true semantics — read-only path, no write side-effect)
//   - the DB call fails (fail-open: better to over-notify than skip a
//     real notification because of a transient pgx error)
//   - category is unrecognized (defensive default; logs a warning so
//     caller misuse is detectable in production)
func (s *Service) ShouldNotify(ctx context.Context, userID uuid.UUID, category Category) bool {
	pref, err := s.prefs.Get(ctx, userID)
	if errors.Is(err, repo.ErrNotFound) {
		return true
	}
	if err != nil {
		return true
	}
	switch category {
	case CategoryDirectMessages:
		return pref.DirectMessages
	case CategoryGroupMessages:
		return pref.GroupMessages
	case CategoryFriendRequests:
		return pref.FriendRequests
	case CategoryCalls:
		return pref.Calls
	}
	slog.WarnContext(ctx, "notificationpref: unknown category, defaulting to notify",
		slog.String("category", string(category)),
		slog.String("user_id", userID.String()),
	)
	return true
}

// UpdateForUser patches whichever fields are non-nil in p. The row is
// created with defaults first if it doesn't yet exist — so a user's
// first-ever PATCH still succeeds. Theme enum values are validated
// here so a bad value returns a 400 with a useful message rather than
// a generic 500 from the DB CHECK constraint.
func (s *Service) UpdateForUser(ctx context.Context, p UpdateParams) (domain.NotificationPreference, error) {
	var fieldErrs []apierror.FieldError
	if p.ThemeScheme != nil {
		if _, ok := validThemeSchemes[*p.ThemeScheme]; !ok {
			fieldErrs = append(fieldErrs, apierror.FieldError{
				Field:   "theme_scheme",
				Code:    "INVALID_VALUE",
				Message: "must be one of: system, sunrise, daylight, noon, golden, meadow, dusk, twilight, aurora, midnight, rem",
			})
		}
	}
	if p.ThemeModePreference != nil {
		if _, ok := validThemeModes[*p.ThemeModePreference]; !ok {
			fieldErrs = append(fieldErrs, apierror.FieldError{
				Field:   "theme_mode_preference",
				Code:    "INVALID_VALUE",
				Message: "must be one of: system, light, dark",
			})
		}
	}
	if len(fieldErrs) > 0 {
		return domain.NotificationPreference{}, apierror.Validation(fieldErrs)
	}
	if _, err := s.prefs.GetOrCreate(ctx, p.UserID); err != nil {
		return domain.NotificationPreference{}, apierror.Internal("ensure notification preferences").WithCause(err)
	}
	pref, err := s.prefs.Patch(ctx, repo.PatchParams{
		UserID:              p.UserID,
		DirectMessages:      p.DirectMessages,
		GroupMessages:       p.GroupMessages,
		FriendRequests:      p.FriendRequests,
		Calls:               p.Calls,
		ThemeScheme:         p.ThemeScheme,
		ThemeModePreference: p.ThemeModePreference,
	})
	if err != nil {
		// ErrNotFound after GetOrCreate would mean the row was deleted
		// concurrently (e.g. user delete cascade) — treat as not-found
		// so the handler returns 404 rather than 500.
		if errors.Is(err, repo.ErrNotFound) {
			return domain.NotificationPreference{}, apierror.NotFound("notification preferences")
		}
		return domain.NotificationPreference{}, apierror.Internal("patch notification preferences").WithCause(err)
	}
	return pref, nil
}
