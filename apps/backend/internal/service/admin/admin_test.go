package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	auditrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/audit"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/admin"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, role string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash, role)
		VALUES ($1, $2, 'T', $3, 'h', $4)
	`, id, "u"+full, full+"@x.test", role)
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

type stack struct {
	svc      *admin.Service
	pool     *pgxpool.Pool
	users    *userrepo.Queries
	auditrep *auditrepo.Queries
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	users := userrepo.New(pool)
	audit := auditrepo.New(pool)
	svc, err := admin.New(admin.Config{Pool: pool, Users: users, Audit: audit})
	if err != nil {
		t.Fatalf("admin.New: %v", err)
	}
	return &stack{svc: svc, pool: pool, users: users, auditrep: audit}
}

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- New() validation ---------------------------------------------------

func TestNew_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	if _, err := admin.New(admin.Config{}); err == nil {
		t.Error("expected error for empty config")
	}
}

// --- ListUsers / GetUser ------------------------------------------------

func TestListUsers_ReturnsActiveUsers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a1 := makeUser(ctx, t, st.pool, "user")
	a2 := makeUser(ctx, t, st.pool, "user")
	deleted := makeUser(ctx, t, st.pool, "user")
	if err := st.users.SoftDelete(ctx, deleted); err != nil {
		t.Fatalf("seed soft delete: %v", err)
	}

	got, err := st.svc.ListUsers(ctx, admin.ListUsersParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got.Users) != 2 {
		t.Errorf("expected 2 active users, got %d", len(got.Users))
	}
	seen := make(map[uuid.UUID]bool, len(got.Users))
	for _, u := range got.Users {
		seen[u.ID] = true
	}
	if !seen[a1] || !seen[a2] {
		t.Errorf("active users missing from list: %+v", got.Users)
	}
	if seen[deleted] {
		t.Errorf("soft-deleted user must be excluded from admin list, got %v", deleted)
	}
}

func TestGetUser_FindsSoftDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st.pool, "user")
	if err := st.users.SoftDelete(ctx, uid); err != nil {
		t.Fatalf("seed soft delete: %v", err)
	}
	got, err := st.svc.GetUser(ctx, uid)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.DeletedAt == nil {
		t.Error("expected deleted user, deleted_at is nil")
	}
}

func TestGetUser_MissingReturns404(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	_, err := st.svc.GetUser(ctx, uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- UpdateRole --------------------------------------------------------

func TestUpdateRole_PromoteAndAuditInTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	got, err := st.svc.UpdateRole(ctx, admin.UpdateRoleParams{
		ActorID: actor, UserID: target, Role: "admin",
	})
	if err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	if got.Role != "admin" {
		t.Errorf("Role = %q, want admin", got.Role)
	}
	// Audit row was written in the same tx — present after the call commits.
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	a := rows[0]
	if a.Action != admin.ActionUserUpdateRole {
		t.Errorf("Action = %q", a.Action)
	}
	if a.ActorID == nil || *a.ActorID != actor {
		t.Errorf("ActorID = %v, want %v", a.ActorID, actor)
	}
	if a.TargetID == nil || *a.TargetID != target {
		t.Errorf("TargetID = %v, want %v", a.TargetID, target)
	}
	if a.Metadata["new_role"] != "admin" {
		t.Errorf("metadata.new_role = %v", a.Metadata["new_role"])
	}
	if a.Metadata["prev_role"] != "user" {
		t.Errorf("metadata.prev_role = %v", a.Metadata["prev_role"])
	}
}

func TestUpdateRole_BogusRoleRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	_, err := st.svc.UpdateRole(ctx, admin.UpdateRoleParams{
		ActorID: actor, UserID: target, Role: "superadmin",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

func TestUpdateRole_MissingTargetReturns404(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")

	_, err := st.svc.UpdateRole(ctx, admin.UpdateRoleParams{
		ActorID: actor, UserID: uuid.New(), Role: "admin",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- SoftDeleteUser ----------------------------------------------------

func TestSoftDeleteUser_DeletesAndAudits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	got, err := st.svc.SoftDeleteUser(ctx, admin.SoftDeleteParams{
		ActorID: actor, UserID: target,
	})
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if got.DeletedAt == nil {
		t.Error("DeletedAt should be set")
	}
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != admin.ActionUserSoftDelete {
		t.Errorf("expected single soft_delete audit row, got %+v", rows)
	}
}

func TestSoftDeleteUser_IdempotentOnSecondCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	if _, err := st.svc.SoftDeleteUser(ctx, admin.SoftDeleteParams{
		ActorID: actor, UserID: target,
	}); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// Second call: target is already soft-deleted. Should NOT error and
	// should NOT write a duplicate audit row.
	if _, err := st.svc.SoftDeleteUser(ctx, admin.SoftDeleteParams{
		ActorID: actor, UserID: target,
	}); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 audit row (no duplicate), got %d", len(rows))
	}
}

func TestSoftDeleteUser_MissingReturns404(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")

	_, err := st.svc.SoftDeleteUser(ctx, admin.SoftDeleteParams{
		ActorID: actor, UserID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- ListAudit ---------------------------------------------------------

func TestListAudit_NewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "admin")

	targets := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		target := makeUser(ctx, t, st.pool, "user")
		targets = append(targets, target)
		if _, err := st.svc.UpdateRole(ctx, admin.UpdateRoleParams{
			ActorID: actor, UserID: target, Role: "admin",
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got, err := st.svc.ListAudit(ctx, admin.ListAuditParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got.Entries))
	}
	// Newest-first: index 0 is the LAST mutation (targets[2]),
	// index 2 is the FIRST mutation (targets[0]).
	for i, want := range []uuid.UUID{targets[2], targets[1], targets[0]} {
		e := got.Entries[i]
		if e.Action != admin.ActionUserUpdateRole {
			t.Errorf("entries[%d].Action = %q, want %q", i, e.Action, admin.ActionUserUpdateRole)
		}
		if e.TargetID == nil || *e.TargetID != want {
			t.Errorf("entries[%d].TargetID = %v, want %v (proves newest-first)", i, e.TargetID, want)
		}
	}
}

// --- StartImpersonation §8.7 matrix ------------------------------------

func TestStartImpersonation_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	admin1 := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	got, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: admin1, TargetID: target,
	})
	if err != nil {
		t.Fatalf("StartImpersonation: %v", err)
	}
	if got.ID != target {
		t.Errorf("returned user is wrong: %v", got.ID)
	}
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != admin.ActionImpersonateStarted {
		t.Fatalf("expected impersonate.started audit row, got %+v", rows)
	}
	if rows[0].Metadata["impersonating_user_id"] != target.String() {
		t.Errorf("metadata.impersonating_user_id = %v", rows[0].Metadata["impersonating_user_id"])
	}
	// actor_id is the real admin, NOT the target. (§8.7 critical rule.)
	if rows[0].ActorID == nil || *rows[0].ActorID != admin1 {
		t.Errorf("ActorID = %v, want admin %v", rows[0].ActorID, admin1)
	}
}

func TestStartImpersonation_NonAdminActorForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	actor := makeUser(ctx, t, st.pool, "user") // not admin
	target := makeUser(ctx, t, st.pool, "user")

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: actor, TargetID: target,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
	// And no audit row should have been written for a denied call.
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("denied StartImpersonation must not write audit; got %+v", rows)
	}
}

func TestStartImpersonation_SelfRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: a, TargetID: a,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestStartImpersonation_AnotherAdminForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")
	other := makeUser(ctx, t, st.pool, "admin")

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: a, TargetID: other,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

func TestStartImpersonation_SoftDeletedTargetReturns404(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")
	if err := st.users.SoftDelete(ctx, target); err != nil {
		t.Fatalf("seed soft delete: %v", err)
	}

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: a, TargetID: target,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

func TestStartImpersonation_MissingTargetReturns404(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{
		ActorID: a, TargetID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- EndImpersonation ---------------------------------------------------

func TestEndImpersonation_WritesBookend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	if err := st.svc.EndImpersonation(ctx, admin.EndImpersonationParams{
		ActorID: a, TargetID: target,
	}); err != nil {
		t.Fatalf("EndImpersonation: %v", err)
	}
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != admin.ActionImpersonateEnded {
		t.Fatalf("expected impersonate.ended audit row, got %+v", rows)
	}
	if rows[0].ActorID == nil || *rows[0].ActorID != a {
		t.Errorf("ActorID = %v, want %v", rows[0].ActorID, a)
	}
	if rows[0].Metadata["impersonating_user_id"] != target.String() {
		t.Errorf("metadata.impersonating_user_id = %v", rows[0].Metadata["impersonating_user_id"])
	}
}

// Bookend pair: Start → End must produce exactly 2 audit rows in order.
func TestImpersonation_BookendPairOrdered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st.pool, "admin")
	target := makeUser(ctx, t, st.pool, "user")

	_, err := st.svc.StartImpersonation(ctx, admin.StartImpersonationParams{ActorID: a, TargetID: target})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := st.svc.EndImpersonation(ctx, admin.EndImpersonationParams{ActorID: a, TargetID: target}); err != nil {
		t.Fatalf("End: %v", err)
	}
	rows, err := st.auditrep.List(ctx, auditrepo.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit rows, got %d", len(rows))
	}
	// List is newest-first → impersonate.ended at index 0, started at 1.
	if rows[0].Action != admin.ActionImpersonateEnded || rows[1].Action != admin.ActionImpersonateStarted {
		t.Errorf("ordering wrong: %q then %q", rows[0].Action, rows[1].Action)
	}
}

// Compile-time guard: ListUsersResult.Users matches the slice type the
// caller iterates. Catches an accidental rename in either side.
var _ []domain.User = (admin.ListUsersResult{}).Users
