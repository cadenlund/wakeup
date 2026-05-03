package presence_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/presence"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL — same pattern as the other
// repo tests in this codebase, kept self-contained.
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

// --- Get + ErrNotFound ------------------------------------------------

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	_, err := repo.Get(ctx, uuid.New())
	if !errors.Is(err, presence.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- UpsertHeartbeat --------------------------------------------------

func TestUpsertHeartbeat_FirstCallCreatesOnlineRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	got, err := repo.UpsertHeartbeat(ctx, uid)
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}
	if got.Status != domain.PresenceOnline {
		t.Errorf("Status = %q, want online", got.Status)
	}
	if got.LastActiveAt.IsZero() || got.LastHeartbeatAt.IsZero() {
		t.Errorf("timestamps zero: %+v", got)
	}
}

// Heartbeat after the away decay should restore to online (§9.2 says
// any heartbeat resurrects an away user).
func TestUpsertHeartbeat_AwayPromotesToOnline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.UpsertHeartbeat(ctx, uid); err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}
	// Demote to 'away' by hand.
	if _, err := pool.Exec(ctx,
		`UPDATE presence_states SET status='away' WHERE user_id=$1`, uid,
	); err != nil {
		t.Fatalf("force away: %v", err)
	}
	got, err := repo.UpsertHeartbeat(ctx, uid)
	if err != nil {
		t.Fatalf("re-heartbeat: %v", err)
	}
	if got.Status != domain.PresenceOnline {
		t.Errorf("Status = %q, want online (heartbeat resurrects away)", got.Status)
	}
}

// Heartbeat must NOT override `sleeping` or `offline` — those are
// manual statuses the user explicitly set.
func TestUpsertHeartbeat_PreservesManualSleeping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.SetStatus(ctx, uid, domain.PresenceSleeping); err != nil {
		t.Fatalf("SetStatus sleeping: %v", err)
	}
	got, err := repo.UpsertHeartbeat(ctx, uid)
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}
	if got.Status != domain.PresenceSleeping {
		t.Errorf("Status = %q, want sleeping (manual override is sticky)", got.Status)
	}
}

func TestUpsertHeartbeat_PreservesManualOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.SetStatus(ctx, uid, domain.PresenceOffline); err != nil {
		t.Fatalf("SetStatus offline: %v", err)
	}
	got, err := repo.UpsertHeartbeat(ctx, uid)
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}
	if got.Status != domain.PresenceOffline {
		t.Errorf("Status = %q, want offline (manual override is sticky)", got.Status)
	}
}

// --- SetStatus --------------------------------------------------------

func TestSetStatus_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	for _, status := range []domain.PresenceStatus{
		domain.PresenceOnline, domain.PresenceAway,
		domain.PresenceSleeping, domain.PresenceOffline,
	} {
		got, err := repo.SetStatus(ctx, uid, status)
		if err != nil {
			t.Fatalf("SetStatus %s: %v", status, err)
		}
		if got.Status != status {
			t.Errorf("Status = %q, want %q", got.Status, status)
		}
	}
}

func TestSetStatus_RejectsInvalidStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)
	_, err := repo.SetStatus(ctx, uid, domain.PresenceStatus("bogus"))
	if err == nil {
		t.Fatal("expected CHECK violation for unknown status")
	}
}

// --- ListByIDs --------------------------------------------------------

func TestListByIDs_ReturnsRowsForKnownUsers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	c := makeUser(ctx, t, pool) // never seeded — should not show up

	if _, err := repo.UpsertHeartbeat(ctx, a); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := repo.SetStatus(ctx, b, domain.PresenceSleeping); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	got, err := repo.ListByIDs(ctx, []uuid.UUID{a, b, c})
	if err != nil {
		t.Fatalf("ListByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (c has no row)", len(got))
	}
}

func TestListByIDs_EmptySliceNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	got, err := repo.ListByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("ListByIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// --- DecayStale -------------------------------------------------------

// Online row older than the cutoff demotes to away; row younger
// than the cutoff is left alone.
func TestDecayStale_OnlineToAway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	stale := makeUser(ctx, t, pool)
	fresh := makeUser(ctx, t, pool)

	if _, err := repo.UpsertHeartbeat(ctx, stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if _, err := repo.UpsertHeartbeat(ctx, fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	// Backdate stale.last_active_at well past the 5-minute cutoff.
	if _, err := pool.Exec(ctx,
		`UPDATE presence_states SET last_active_at = $2 WHERE user_id = $1`,
		stale, time.Now().Add(-10*time.Minute),
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	demoted, err := repo.DecayStale(ctx, 5*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("DecayStale: %v", err)
	}
	if len(demoted) != 1 || demoted[0].UserID != stale {
		t.Fatalf("demoted = %+v, want [stale]", demoted)
	}
	if demoted[0].Status != domain.PresenceAway {
		t.Errorf("Status = %q, want away", demoted[0].Status)
	}
	// Fresh row untouched.
	got, _ := repo.Get(ctx, fresh)
	if got.Status != domain.PresenceOnline {
		t.Errorf("fresh demoted to %q, want online", got.Status)
	}
}

// Away row older than the cutoff demotes to offline.
func TestDecayStale_AwayToOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.SetStatus(ctx, uid, domain.PresenceAway); err != nil {
		t.Fatalf("seed away: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE presence_states SET last_active_at = $2 WHERE user_id = $1`,
		uid, time.Now().Add(-2*time.Hour),
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	demoted, err := repo.DecayStale(ctx, 5*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("DecayStale: %v", err)
	}
	if len(demoted) != 1 || demoted[0].Status != domain.PresenceOffline {
		t.Errorf("demoted = %+v, want one offline", demoted)
	}
}

// `sleeping` and `offline` rows are NEVER auto-demoted — manual
// status sticks across the sweeper.
func TestDecayStale_LeavesManualStatusAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	sleeper := makeUser(ctx, t, pool)
	offline := makeUser(ctx, t, pool)

	if _, err := repo.SetStatus(ctx, sleeper, domain.PresenceSleeping); err != nil {
		t.Fatalf("seed sleeper: %v", err)
	}
	if _, err := repo.SetStatus(ctx, offline, domain.PresenceOffline); err != nil {
		t.Fatalf("seed offline: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE presence_states SET last_active_at = $1`,
		time.Now().Add(-10*time.Hour),
	); err != nil {
		t.Fatalf("backdate all: %v", err)
	}

	demoted, err := repo.DecayStale(ctx, 5*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("DecayStale: %v", err)
	}
	if len(demoted) != 0 {
		t.Errorf("demoted = %+v, want 0 (sleeping/offline are sticky)", demoted)
	}
}

func TestDecayStale_EmptyTableNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := presence.New(pool)
	got, err := repo.DecayStale(ctx, 5*time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("DecayStale: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// --- IsValid (domain helper) ------------------------------------------

func TestPresenceStatus_IsValid(t *testing.T) {
	t.Parallel()
	for _, s := range []domain.PresenceStatus{
		domain.PresenceOnline, domain.PresenceAway,
		domain.PresenceOffline, domain.PresenceSleeping,
	} {
		if !s.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", s)
		}
	}
	if domain.PresenceStatus("bogus").IsValid() {
		t.Errorf("IsValid(bogus) = true, want false")
	}
}
