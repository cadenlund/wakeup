package notification_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/service/notification"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a minimal users row so foreign keys on
// presence_states / conversation_members hold.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test"); err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

// makeDirectConv creates a direct conversation with two members so a
// per-member mute toggle has somewhere to land.
func makeDirectConv(ctx context.Context, t *testing.T, pool *pgxpool.Pool, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		INSERT INTO conversations (id, type, created_by) VALUES ($1, 'direct', $2)
	`, id, a); err != nil {
		t.Fatalf("create conv: %v", err)
	}
	for _, m := range []uuid.UUID{a, b} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role)
			VALUES ($1, $2, 'member')
		`, id, m); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	return id
}

func TestPushSuppressed_Default_NotSuppressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	uid := makeUser(ctx, t, pool)

	s, err := notification.NewPushSuppression(pool)
	if err != nil {
		t.Fatalf("NewPushSuppression: %v", err)
	}
	got, err := s.PushSuppressed(ctx, uid, nil)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if got {
		t.Errorf("user with no presence row + no convID should not be suppressed")
	}
}

func TestPushSuppressed_DNDIntent_SuppressedRegardlessOfConv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	uid := makeUser(ctx, t, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO presence_states (user_id, status, intent)
		VALUES ($1, 'dnd', 'dnd')
	`, uid); err != nil {
		t.Fatalf("seed dnd: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	// nil convID — DND alone should suppress.
	got, err := s.PushSuppressed(ctx, uid, nil)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if !got {
		t.Errorf("DND user should be suppressed for non-conv pushes")
	}
}

func TestPushSuppressed_NonDNDIntent_NotSuppressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	uid := makeUser(ctx, t, pool)

	// 'sleeping' is sticky but does NOT suppress pushes — only DND does.
	if _, err := pool.Exec(ctx, `
		INSERT INTO presence_states (user_id, status, intent)
		VALUES ($1, 'sleeping', 'sleeping')
	`, uid); err != nil {
		t.Fatalf("seed sleeping: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	got, err := s.PushSuppressed(ctx, uid, nil)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if got {
		t.Errorf("sleeping intent should NOT suppress pushes (only dnd does)")
	}
}

func TestPushSuppressed_ConvMutedFuture_Suppressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	conv := makeDirectConv(ctx, t, pool, a, b)

	future := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE conversation_members SET muted_until = $3 WHERE conversation_id = $1 AND user_id = $2
	`, conv, a, future); err != nil {
		t.Fatalf("seed mute: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	got, err := s.PushSuppressed(ctx, a, &conv)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if !got {
		t.Errorf("muted_until in future should suppress conv-scoped pushes")
	}
}

func TestPushSuppressed_ConvMutedPast_NotSuppressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	conv := makeDirectConv(ctx, t, pool, a, b)

	past := time.Now().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE conversation_members SET muted_until = $3 WHERE conversation_id = $1 AND user_id = $2
	`, conv, a, past); err != nil {
		t.Fatalf("seed expired mute: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	got, err := s.PushSuppressed(ctx, a, &conv)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if got {
		t.Errorf("expired mute (muted_until in past) should NOT suppress")
	}
}

func TestPushSuppressed_ConvMute_PerMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	conv := makeDirectConv(ctx, t, pool, a, b)

	future := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE conversation_members SET muted_until = $3 WHERE conversation_id = $1 AND user_id = $2
	`, conv, a, future); err != nil {
		t.Fatalf("seed mute: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	// b's row is NOT muted → not suppressed.
	got, err := s.PushSuppressed(ctx, b, &conv)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if got {
		t.Errorf("mute is per-member; b should not be suppressed when only a muted")
	}
}

func TestPushSuppressed_NonConvScope_IgnoresMute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	conv := makeDirectConv(ctx, t, pool, a, b)

	future := time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE conversation_members SET muted_until = $3 WHERE conversation_id = $1 AND user_id = $2
	`, conv, a, future); err != nil {
		t.Fatalf("seed mute: %v", err)
	}

	s, _ := notification.NewPushSuppression(pool)
	// nil convID — friend-request style. Per-conv mute does not apply.
	got, err := s.PushSuppressed(ctx, a, nil)
	if err != nil {
		t.Fatalf("PushSuppressed: %v", err)
	}
	if got {
		t.Errorf("non-conv push should not be gated by per-conv mute")
	}
}
