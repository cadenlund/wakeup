package middleware_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

func TestContextHelpers_RequestID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := mw.RequestIDFromContext(ctx); got != "" {
		t.Errorf("empty ctx should return \"\", got %q", got)
	}
	ctx2 := mw.WithRequestID(ctx, "abc")
	if got := mw.RequestIDFromContext(ctx2); got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}

func TestContextHelpers_User(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if mw.UserFromContext(ctx) != nil {
		t.Error("empty ctx should return nil user")
	}
	u := &domain.User{ID: uuid.New(), Username: "caden", Role: "user"}
	ctx2 := mw.WithUser(ctx, u)
	got := mw.UserFromContext(ctx2)
	if got == nil || got.Username != "caden" {
		t.Errorf("got %+v, want caden", got)
	}
}

func TestContextHelpers_RealUserFallsBackToUser(t *testing.T) {
	t.Parallel()
	u := &domain.User{ID: uuid.New(), Username: "caden", Role: "user"}
	ctx := mw.WithUser(context.Background(), u)
	got := mw.RealUserFromContext(ctx)
	if got == nil || got.ID != u.ID {
		t.Errorf("RealUserFromContext should fall back to UserFromContext, got %+v", got)
	}
}

func TestContextHelpers_RealUserDistinct(t *testing.T) {
	t.Parallel()
	effective := &domain.User{ID: uuid.New(), Username: "target", Role: "user"}
	admin := &domain.User{ID: uuid.New(), Username: "admin", Role: "admin"}
	ctx := mw.WithUser(context.Background(), effective)
	ctx = mw.WithRealUser(ctx, admin)
	if got := mw.UserFromContext(ctx); got.Username != "target" {
		t.Errorf("UserFromContext = %q, want target", got.Username)
	}
	if got := mw.RealUserFromContext(ctx); got.Username != "admin" {
		t.Errorf("RealUserFromContext = %q, want admin", got.Username)
	}
}
