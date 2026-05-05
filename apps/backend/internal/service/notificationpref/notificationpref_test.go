package notificationpref_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL — same trick as the repo tests, to
// satisfy the FK without dragging in the user repository's fixtures.
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
	svc  *notificationpref.Service
	pool *pgxpool.Pool
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	svc, err := notificationpref.New(notificationpref.Config{
		Prefs: repo.New(pool),
	})
	if err != nil {
		t.Fatalf("notificationpref.New: %v", err)
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

// --- GetForUser ----------------------------------------------------------

func TestGetForUser_DefaultsAllTrueOnFirstCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	got, err := st.svc.GetForUser(ctx, uid)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if got.UserID != uid {
		t.Errorf("UserID mismatch")
	}
	if !got.DirectMessages || !got.GroupMessages || !got.FriendRequests || !got.Calls {
		t.Errorf("expected all booleans true, got %+v", got)
	}
	if got.ThemeScheme != "system" {
		t.Errorf("theme_scheme default: want \"system\", got %q", got.ThemeScheme)
	}
	if got.ThemeModePreference != "system" {
		t.Errorf("theme_mode_preference default: want \"system\", got %q", got.ThemeModePreference)
	}
}

func TestGetForUser_IsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	// Mutate after first call; second call must NOT clobber back to defaults.
	if _, err := st.svc.GetForUser(ctx, uid); err != nil {
		t.Fatalf("first GetForUser: %v", err)
	}
	off := false
	if _, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID: uid, Calls: &off,
	}); err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}
	again, err := st.svc.GetForUser(ctx, uid)
	if err != nil {
		t.Fatalf("second GetForUser: %v", err)
	}
	if again.Calls {
		t.Errorf("GetForUser clobbered patched Calls=false back to true")
	}
}

// --- UpdateForUser -------------------------------------------------------

func TestUpdateForUser_AutoCreatesRowOnFirstCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	// Brand-new user — no row in notification_preferences yet. The service
	// must create it via GetOrCreate before patching.
	off := false
	got, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:         uid,
		FriendRequests: &off,
	})
	if err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}
	if got.FriendRequests {
		t.Errorf("FriendRequests should be false, got true")
	}
	// Untouched fields keep the schema-default true.
	if !got.DirectMessages || !got.GroupMessages || !got.Calls {
		t.Errorf("untouched fields changed: %+v", got)
	}
}

func TestUpdateForUser_PartialPreservesUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	off := false
	if _, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID: uid, DirectMessages: &off,
	}); err != nil {
		t.Fatalf("first UpdateForUser: %v", err)
	}
	// Toggle a different field — the previous DirectMessages=false must survive.
	got, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID: uid, GroupMessages: &off,
	})
	if err != nil {
		t.Fatalf("second UpdateForUser: %v", err)
	}
	if got.DirectMessages {
		t.Errorf("DirectMessages clobbered back to true")
	}
	if got.GroupMessages {
		t.Errorf("GroupMessages should be false")
	}
}

func TestUpdateForUser_AllFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	off := false
	scheme := "midnight"
	mode := "dark"
	got, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:              uid,
		DirectMessages:      &off,
		GroupMessages:       &off,
		FriendRequests:      &off,
		Calls:               &off,
		ThemeScheme:         &scheme,
		ThemeModePreference: &mode,
	})
	if err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}
	if got.DirectMessages || got.GroupMessages || got.FriendRequests || got.Calls {
		t.Errorf("expected all booleans false, got %+v", got)
	}
	if got.ThemeScheme != "midnight" {
		t.Errorf("theme_scheme = %q, want \"midnight\"", got.ThemeScheme)
	}
	if got.ThemeModePreference != "dark" {
		t.Errorf("theme_mode_preference = %q, want \"dark\"", got.ThemeModePreference)
	}
}

// Theme is on its own axis — patching just the theme leaves notification
// booleans alone (and vice versa). This is the gallery picker's actual
// usage pattern.
func TestUpdateForUser_ThemeOnlyPreservesNotifications(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	scheme := "aurora"
	got, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:      uid,
		ThemeScheme: &scheme,
	})
	if err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}
	if got.ThemeScheme != "aurora" {
		t.Errorf("theme_scheme not patched: %+v", got)
	}
	if !got.DirectMessages || !got.GroupMessages || !got.FriendRequests || !got.Calls {
		t.Errorf("notification booleans changed: %+v", got)
	}
}

// Service rejects bad enum values with apierror.Validation BEFORE the
// row is touched — so a typo doesn't even hit Postgres.
func TestUpdateForUser_RejectsInvalidThemeScheme(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	bogus := "neon"
	_, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:      uid,
		ThemeScheme: &bogus,
	})
	apiErr := asAPIError(t, err)
	if apiErr.Code != apierror.CodeValidation {
		t.Errorf("expected CodeValidation, got %q", apiErr.Code)
	}
	if len(apiErr.Fields) != 1 || apiErr.Fields[0].Field != "theme_scheme" {
		t.Errorf("expected single FieldError on theme_scheme, got %+v", apiErr.Fields)
	}
}

func TestUpdateForUser_RejectsInvalidThemeMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	bogus := "auto"
	_, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:              uid,
		ThemeModePreference: &bogus,
	})
	apiErr := asAPIError(t, err)
	if len(apiErr.Fields) != 1 || apiErr.Fields[0].Field != "theme_mode_preference" {
		t.Errorf("expected single FieldError on theme_mode_preference, got %+v", apiErr.Fields)
	}
}

func TestUpdateForUser_NoFieldsIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	// Empty patch — service should still ensure-and-return the row at defaults.
	got, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{UserID: uid})
	if err != nil {
		t.Fatalf("UpdateForUser empty: %v", err)
	}
	if !got.DirectMessages || !got.GroupMessages || !got.FriendRequests || !got.Calls {
		t.Errorf("empty patch shouldn't mutate; got %+v", got)
	}
}

// --- New() validation ---------------------------------------------------

func TestNew_RejectsNilPrefs(t *testing.T) {
	t.Parallel()
	if _, err := notificationpref.New(notificationpref.Config{}); err == nil {
		t.Error("expected error for nil Prefs")
	}
}

// --- ShouldNotify --------------------------------------------------------

func TestShouldNotify_DefaultsAllTrueForFreshUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	for _, cat := range []notificationpref.Category{
		notificationpref.CategoryDirectMessages,
		notificationpref.CategoryGroupMessages,
		notificationpref.CategoryFriendRequests,
		notificationpref.CategoryCalls,
	} {
		if !st.svc.ShouldNotify(ctx, uid, cat) {
			t.Errorf("category %q: expected true (default), got false", cat)
		}
	}

	// And critically, the read path should NOT have auto-created a row —
	// the gate is read-only so notification triggers don't pile up writes.
	var count int
	if err := st.pool.QueryRow(ctx,
		"SELECT count(*) FROM notification_preferences WHERE user_id = $1", uid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("ShouldNotify should be read-only; found %d row(s)", count)
	}
}

func TestShouldNotify_RespectsPatchedToggle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	off := false
	if _, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID: uid, Calls: &off,
	}); err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}

	if st.svc.ShouldNotify(ctx, uid, notificationpref.CategoryCalls) {
		t.Errorf("Calls patched off, ShouldNotify(calls) should be false")
	}
	// Other categories untouched.
	for _, cat := range []notificationpref.Category{
		notificationpref.CategoryDirectMessages,
		notificationpref.CategoryGroupMessages,
		notificationpref.CategoryFriendRequests,
	} {
		if !st.svc.ShouldNotify(ctx, uid, cat) {
			t.Errorf("category %q should still be true after patching only Calls", cat)
		}
	}
}

func TestShouldNotify_AllCategoriesIndependent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	off := false
	on := true
	if _, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:         uid,
		DirectMessages: &off,
		GroupMessages:  &on,
		FriendRequests: &off,
		Calls:          &on,
	}); err != nil {
		t.Fatalf("UpdateForUser: %v", err)
	}

	cases := map[notificationpref.Category]bool{
		notificationpref.CategoryDirectMessages: false,
		notificationpref.CategoryGroupMessages:  true,
		notificationpref.CategoryFriendRequests: false,
		notificationpref.CategoryCalls:          true,
	}
	for cat, want := range cases {
		if got := st.svc.ShouldNotify(ctx, uid, cat); got != want {
			t.Errorf("ShouldNotify(%q) = %v, want %v", cat, got, want)
		}
	}
}

func TestShouldNotify_FailsOpenOnDBError(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Canceled ctx → pgx returns an error → ShouldNotify must fail open.
	if !st.svc.ShouldNotify(ctx, uuid.New(), notificationpref.CategoryDirectMessages) {
		t.Error("expected ShouldNotify to fail open (true) on DB error")
	}
}

func TestShouldNotify_UnknownCategoryDefaultsTrue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool)

	// An unrecognized category shouldn't drop a notification — fail-open
	// is consistent with the rest of ShouldNotify.
	if !st.svc.ShouldNotify(ctx, uid, notificationpref.Category("nonexistent")) {
		t.Error("expected unknown category to default true")
	}
}

// --- error mapping -------------------------------------------------------

// canceledCtx error path: a canceled context surfaces from pgx as a
// non-ErrNotFound error, and the service should wrap it as Internal.
func TestGetForUser_WrapsErrorsAsInternal(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := st.svc.GetForUser(ctx, uuid.New())
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if asAPIError(t, err).Code != apierror.CodeInternal {
		t.Errorf("Code = %q, want INTERNAL", asAPIError(t, err).Code)
	}
}

// UpdateForUser surfaces apierror.Internal when the underlying
// GetOrCreate fails — covers the upsert-error branch the
// auto-creates-row test can't reach.
func TestUpdateForUser_GetOrCreateErrorWrappedAsInternal(t *testing.T) {
	t.Parallel()
	st := newStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := st.svc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:         uuid.New(),
		DirectMessages: ptr(false),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeInternal {
		t.Errorf("Code = %q, want INTERNAL", asAPIError(t, err).Code)
	}
}

func ptr[T any](v T) *T { return &v }
