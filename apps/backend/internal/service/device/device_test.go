package device_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/devicetoken"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/device"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL — same trick used in repo_test.go to
// keep this package's fixtures dependency-free.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

type stack struct {
	svc  *device.Service
	pool *pgxpool.Pool
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	svc, err := device.New(device.Config{Devices: repo.New(pool)})
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}
	return &stack{svc: svc, pool: pool}
}

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- Register ------------------------------------------------------------

func TestRegister_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	got, err := st.svc.Register(ctx, uid, "ExponentPushToken[a]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.UserID != uid || got.ExpoToken != "ExponentPushToken[a]" || got.Platform != domain.DeviceIOS {
		t.Errorf("unexpected row: %+v", got)
	}
}

func TestRegister_RejectsUnknownPlatform(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	_, err := st.svc.Register(ctx, uid, "ExponentPushToken[a]", domain.DevicePlatform("blackberry"))
	if err == nil {
		t.Fatal("expected error for unknown platform")
	}
	if asAPIError(t, err).Code != apierror.CodeBadRequest {
		t.Errorf("Code = %q, want BAD_REQUEST", asAPIError(t, err).Code)
	}
}

func TestRegister_RejectsEmptyToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	for _, tok := range []string{"", "   ", "\t\n"} {
		_, err := st.svc.Register(ctx, uid, tok, domain.DeviceIOS)
		if err == nil {
			t.Fatalf("expected error for empty/whitespace token %q", tok)
		}
		if asAPIError(t, err).Code != apierror.CodeBadRequest {
			t.Errorf("token %q: Code = %q, want BAD_REQUEST", tok, asAPIError(t, err).Code)
		}
	}
}

func TestRegister_IdempotentReregister(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	first, err := st.svc.Register(ctx, uid, "ExponentPushToken[same]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	second, err := st.svc.Register(ctx, uid, "ExponentPushToken[same]", domain.DeviceAndroid)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-register should return same id: first=%v second=%v", first.ID, second.ID)
	}
	if second.Platform != domain.DeviceAndroid {
		t.Errorf("expected platform refresh, got %q", second.Platform)
	}

	// Single row total.
	var count int
	if err := st.pool.QueryRow(ctx,
		"SELECT count(*) FROM device_tokens WHERE user_id = $1", uid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// --- Delete --------------------------------------------------------------

func TestDelete_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	tok, err := st.svc.Register(ctx, uid, "ExponentPushToken[x]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := st.svc.Delete(ctx, uid, tok.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDelete_MissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	err := st.svc.Delete(ctx, uid, uuid.New())
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestDelete_OtherUsersTokenReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	owner := makeUser(ctx, t, st.pool)
	thief := makeUser(ctx, t, st.pool)

	tok, err := st.svc.Register(ctx, owner, "ExponentPushToken[scoped]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Thief tries to delete owner's token by id. The repo's (id, user_id)
	// scoping ensures no row matches → NotFound (no enumeration leak).
	err = st.svc.Delete(ctx, thief, tok.ID)
	if err == nil {
		t.Fatal("expected error when thief deletes owner's token")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want NOT_FOUND", asAPIError(t, err).Code)
	}
	// Token must still exist for the owner.
	var count int
	if err := st.pool.QueryRow(ctx,
		"SELECT count(*) FROM device_tokens WHERE id = $1 AND user_id = $2",
		tok.ID, owner,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("token shouldn't have been removed, count=%d", count)
	}
}

// --- New() validation ---------------------------------------------------

func TestNew_RejectsNilDevices(t *testing.T) {
	t.Parallel()
	if _, err := device.New(device.Config{}); err == nil {
		t.Error("expected error for nil Devices")
	}
}
