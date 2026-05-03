// Package admin is the §8.7 admin service. Exposes the privileged
// operations the admin handlers in §12.5 need:
//
//   - ListUsers / GetUser    : audit-friendly user lookup (GetUser
//     includes soft-deleted rows so admins can find them).
//   - UpdateRole             : promote/demote a user (audit row written
//     in the same tx as the row update).
//   - SoftDeleteUser         : admin-driven deletion. Re-deleting a
//     soft-deleted user is a no-op rather than an error (idempotent
//     behaviour matches the §12.5 PATCH semantics).
//   - ListAudit              : paginated read of audit_log.
//   - StartImpersonation /
//     EndImpersonation       : §8.7 bookends. Validates the §8.7
//     restriction matrix (no self / admin / soft-deleted target) and
//     writes the audit bookend; the actual scs session field is written
//     by the handler since scs is HTTP-scoped middleware.
//
// Every method that mutates state writes an audit_log row. Per §8.7
// the actor_id on every audit row is the *real* admin's id — never the
// impersonated user's id, even when the action originates during an
// impersonation session. The handler passes both via params so this
// package doesn't need to peek at the session itself.
package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	auditrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/audit"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// roleAdmin matches the schema's CHECK constraint on users.role.
const roleAdmin = "admin"

// Action constants used for the audit_log.action column.
const (
	ActionUserUpdateRole     = "user.update_role"
	ActionUserSoftDelete     = "user.soft_delete"
	ActionImpersonateStarted = "impersonate.started"
	ActionImpersonateEnded   = "impersonate.ended"
)

// Metadata key constants for the audit_log.metadata jsonb column.
// Exported so the §12.5 admin handlers and tests can assert on the
// same strings the service writes.
const (
	MetadataImpersonatingUserID = "impersonating_user_id"
	MetadataNewRole             = "new_role"
	MetadataPrevRole            = "prev_role"
)

// Service composes the user + audit repositories. Every mutation that
// produces an audit row runs inside a transaction so a failed audit
// write rolls the underlying mutation back.
type Service struct {
	pool  *pgxpool.Pool
	users *userrepo.Queries
	audit *auditrepo.Queries
}

// Config builds the service.
type Config struct {
	Pool  *pgxpool.Pool
	Users *userrepo.Queries
	Audit *auditrepo.Queries
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Pool == nil {
		return nil, errors.New("admin: Config.Pool is required")
	}
	if cfg.Users == nil {
		return nil, errors.New("admin: Config.Users is required")
	}
	if cfg.Audit == nil {
		return nil, errors.New("admin: Config.Audit is required")
	}
	return &Service{pool: cfg.Pool, users: cfg.Users, audit: cfg.Audit}, nil
}

// --- Reads ---------------------------------------------------------------

// ListUsersParams is the input to ListUsers.
//
// Soft-deleted users are NOT returned — that's a known limitation of
// the underlying user repo's ListByPrefix. Admins can still fetch
// soft-deleted users individually via GetUser, which uses
// GetByIDIncludingDeleted.
type ListUsersParams struct {
	Query  string
	Cursor *pagination.Cursor
	Limit  int
}

// ListUsersResult is the paginated payload.
type ListUsersResult struct {
	Users      []domain.User
	NextCursor *string
	HasMore    bool
}

// ListUsers delegates to the user repo's prefix search.
func (s *Service) ListUsers(ctx context.Context, p ListUsersParams) (ListUsersResult, error) {
	overFetched, err := s.users.ListByPrefix(ctx, p.Query, p.Cursor, p.Limit)
	if err != nil {
		return ListUsersResult{}, apierror.Internal("admin: list users").WithCause(err)
	}
	data, next, hasMore := pagination.Page(overFetched, p.Limit, func(u domain.User) pagination.Cursor {
		return pagination.Cursor{Timestamp: u.CreatedAt, ID: u.ID}
	})
	return ListUsersResult{Users: data, NextCursor: next, HasMore: hasMore}, nil
}

// GetUser returns the user including soft-deleted rows so admins can
// look up users they (or a sweeper) deleted.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (domain.User, error) {
	u, err := s.users.GetByIDIncludingDeleted(ctx, id)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("admin: get user").WithCause(err)
	}
	return u, nil
}

// ListAuditParams is the input to ListAudit.
type ListAuditParams struct {
	Cursor *pagination.Cursor
	Limit  int
}

// ListAuditResult is the paginated payload.
type ListAuditResult struct {
	Entries    []domain.AuditLog
	NextCursor *string
	HasMore    bool
}

// ListAudit returns audit_log rows newest-first. Pure read, no audit
// row written.
func (s *Service) ListAudit(ctx context.Context, p ListAuditParams) (ListAuditResult, error) {
	overFetched, err := s.audit.List(ctx, auditrepo.ListParams{Cursor: p.Cursor, Limit: p.Limit})
	if err != nil {
		return ListAuditResult{}, apierror.Internal("admin: list audit").WithCause(err)
	}
	data, next, hasMore := pagination.Page(overFetched, p.Limit, func(a domain.AuditLog) pagination.Cursor {
		return pagination.Cursor{Timestamp: a.CreatedAt, ID: a.ID}
	})
	return ListAuditResult{Entries: data, NextCursor: next, HasMore: hasMore}, nil
}

// --- Writes --------------------------------------------------------------

// UpdateRoleParams is the input to UpdateRole.
type UpdateRoleParams struct {
	ActorID uuid.UUID // the real admin (RealUser, never the impersonated id)
	UserID  uuid.UUID
	Role    string // "user" or "admin"
}

// UpdateRole sets a new role on the target user and writes the audit
// row in the same transaction. Returns 422 for an unknown role and
// 404 when the target is missing or soft-deleted (UpdateRole's WHERE
// clause excludes deleted users).
func (s *Service) UpdateRole(ctx context.Context, p UpdateRoleParams) (domain.User, error) {
	if p.Role != "user" && p.Role != roleAdmin {
		return domain.User{}, apierror.Validation([]apierror.FieldError{{
			Field: "role", Code: "INVALID_VALUE",
			Message: `role must be one of: "user", "admin"`,
		}})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	users := s.users.WithTx(tx)
	audit := s.audit.WithTx(tx)

	// Read prev INSIDE the tx so a concurrent role update can't race
	// the audit row with a stale prev_role. (CodeRabbit PR #68.)
	prev, err := users.GetByID(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("admin: lookup target").WithCause(err)
	}
	if err := users.UpdateRole(ctx, p.UserID, p.Role); err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("admin: update role").WithCause(err)
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, apierror.Internal("admin: uuid").WithCause(err)
	}
	if err := audit.Create(ctx, auditrepo.CreateParams{
		ID: auditID, ActorID: &p.ActorID, Action: ActionUserUpdateRole,
		TargetType: ptrStr("user"), TargetID: &p.UserID,
		Metadata: map[string]any{
			MetadataPrevRole: prev.Role,
			MetadataNewRole:  p.Role,
		},
	}); err != nil {
		return domain.User{}, apierror.Internal("admin: write audit").WithCause(err)
	}
	updated, err := users.GetByID(ctx, p.UserID)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: re-fetch user").WithCause(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, apierror.Internal("admin: commit").WithCause(err)
	}
	return updated, nil
}

// SoftDeleteParams is the input to SoftDeleteUser.
type SoftDeleteParams struct {
	ActorID uuid.UUID
	UserID  uuid.UUID
}

// UpdateUserParams is the input to UpdateUser. Pointer fields use
// nil-means-unchanged semantics; pass at least one non-nil field.
type UpdateUserParams struct {
	ActorID    uuid.UUID
	UserID     uuid.UUID
	Role       *string
	SoftDelete bool // true → soft-delete in this tx
}

// UpdateUser is the §12.5 PATCH-shaped admin update. Runs role update
// (if Role != nil) AND soft-delete (if SoftDelete) in a single
// transaction so a partial failure can't leave the user in a half-
// updated state. Each branch writes its own audit row inside the tx.
//
// Validation matches UpdateRole / SoftDeleteUser when each is enabled
// (unknown role → 422, missing user → 404, etc.). At least one field
// must be set; an all-nil params errors with 422.
func (s *Service) UpdateUser(ctx context.Context, p UpdateUserParams) (domain.User, error) {
	if p.Role == nil && !p.SoftDelete {
		return domain.User{}, apierror.Validation([]apierror.FieldError{{
			Field: "request", Code: "REQUIRED",
			Message: "at least one of role / deleted_at must be supplied",
		}})
	}
	if p.Role != nil && *p.Role != "user" && *p.Role != roleAdmin {
		return domain.User{}, apierror.Validation([]apierror.FieldError{{
			Field: "role", Code: "INVALID_VALUE",
			Message: `role must be one of: "user", "admin"`,
		}})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	users := s.users.WithTx(tx)
	audit := s.audit.WithTx(tx)

	// Resolve target inside the tx so the prev_role / existence check is
	// consistent with the rest of the writes (CodeRabbit caught the
	// outside-tx race on PR #68).
	prev, err := users.GetByID(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("admin: lookup target").WithCause(err)
	}

	if p.Role != nil {
		if err := users.UpdateRole(ctx, p.UserID, *p.Role); err != nil {
			if errors.Is(err, userrepo.ErrNotFound) {
				return domain.User{}, apierror.NotFound("user")
			}
			return domain.User{}, apierror.Internal("admin: update role").WithCause(err)
		}
		auditID, err := uuid.NewV7()
		if err != nil {
			return domain.User{}, apierror.Internal("admin: uuid").WithCause(err)
		}
		if err := audit.Create(ctx, auditrepo.CreateParams{
			ID: auditID, ActorID: &p.ActorID, Action: ActionUserUpdateRole,
			TargetType: ptrStr("user"), TargetID: &p.UserID,
			Metadata: map[string]any{
				MetadataPrevRole: prev.Role,
				MetadataNewRole:  *p.Role,
			},
		}); err != nil {
			return domain.User{}, apierror.Internal("admin: write audit").WithCause(err)
		}
	}

	if p.SoftDelete {
		// SoftDelete WHERE excludes already-deleted rows; in the tx-
		// branch we tolerate that and skip the audit so the call stays
		// idempotent (matches SoftDeleteUser's behavior).
		if err := users.SoftDelete(ctx, p.UserID); err != nil {
			if !errors.Is(err, userrepo.ErrNotFound) {
				return domain.User{}, apierror.Internal("admin: soft delete").WithCause(err)
			}
		} else {
			auditID, err := uuid.NewV7()
			if err != nil {
				return domain.User{}, apierror.Internal("admin: uuid").WithCause(err)
			}
			if err := audit.Create(ctx, auditrepo.CreateParams{
				ID: auditID, ActorID: &p.ActorID, Action: ActionUserSoftDelete,
				TargetType: ptrStr("user"), TargetID: &p.UserID,
			}); err != nil {
				return domain.User{}, apierror.Internal("admin: write audit").WithCause(err)
			}
		}
	}

	updated, err := users.GetByIDIncludingDeleted(ctx, p.UserID)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: re-fetch user").WithCause(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, apierror.Internal("admin: commit").WithCause(err)
	}
	return updated, nil
}

// SoftDeleteUser sets deleted_at = now() on the target. Idempotent:
// re-deleting an already-deleted user returns the row without writing
// a duplicate audit entry, since the underlying SoftDelete is a no-op
// in that case (its WHERE excludes deleted rows). The mutation +
// audit write run in one transaction.
func (s *Service) SoftDeleteUser(ctx context.Context, p SoftDeleteParams) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	users := s.users.WithTx(tx)
	audit := s.audit.WithTx(tx)

	if err := users.SoftDelete(ctx, p.UserID); err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			// Already deleted (or never existed). Idempotent: surface
			// the row if it exists, 404 otherwise. Re-fetch via
			// IncludingDeleted so a deleted-already row still resolves.
			existing, getErr := users.GetByIDIncludingDeleted(ctx, p.UserID)
			if getErr != nil {
				if errors.Is(getErr, userrepo.ErrNotFound) {
					return domain.User{}, apierror.NotFound("user")
				}
				return domain.User{}, apierror.Internal("admin: re-fetch user").WithCause(getErr)
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.User{}, apierror.Internal("admin: commit").WithCause(err)
			}
			return existing, nil
		}
		return domain.User{}, apierror.Internal("admin: soft delete").WithCause(err)
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, apierror.Internal("admin: uuid").WithCause(err)
	}
	if err := audit.Create(ctx, auditrepo.CreateParams{
		ID: auditID, ActorID: &p.ActorID, Action: ActionUserSoftDelete,
		TargetType: ptrStr("user"), TargetID: &p.UserID,
	}); err != nil {
		return domain.User{}, apierror.Internal("admin: write audit").WithCause(err)
	}
	updated, err := users.GetByIDIncludingDeleted(ctx, p.UserID)
	if err != nil {
		return domain.User{}, apierror.Internal("admin: re-fetch user").WithCause(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, apierror.Internal("admin: commit").WithCause(err)
	}
	return updated, nil
}

// --- Impersonation -------------------------------------------------------

// StartImpersonationParams is the input to StartImpersonation.
type StartImpersonationParams struct {
	ActorID  uuid.UUID // the real admin
	TargetID uuid.UUID // who to impersonate
}

// StartImpersonation validates the §8.7 restriction matrix and writes
// the audit bookend. Returns the target user so the handler can render
// MeResponse + record `impersonating_user_id` in the scs session.
//
// Mapping of failures to HTTP status (per §8.7):
//
//	non-admin actor          → 403 FORBIDDEN
//	target == actor          → 422 VALIDATION_FAILED
//	target is admin          → 403 FORBIDDEN (no privilege escalation)
//	target missing / deleted → 404 RESOURCE_NOT_FOUND
func (s *Service) StartImpersonation(ctx context.Context, p StartImpersonationParams) (domain.User, error) {
	if p.ActorID == p.TargetID {
		return domain.User{}, apierror.Validation([]apierror.FieldError{{
			Field: "id", Code: "INVALID_VALUE",
			Message: "cannot impersonate yourself",
		}})
	}
	actor, err := s.users.GetByID(ctx, p.ActorID)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.Forbidden("admin role required")
		}
		return domain.User{}, apierror.Internal("admin: lookup actor").WithCause(err)
	}
	if actor.Role != roleAdmin {
		return domain.User{}, apierror.Forbidden("admin role required")
	}
	target, err := s.users.GetByID(ctx, p.TargetID)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("admin: lookup target").WithCause(err)
	}
	if target.Role == roleAdmin {
		return domain.User{}, apierror.Forbidden("cannot impersonate another admin")
	}

	if err := s.writeImpersonationBookend(ctx, p.ActorID, p.TargetID, ActionImpersonateStarted); err != nil {
		return domain.User{}, err
	}
	return target, nil
}

// EndImpersonationParams is the input to EndImpersonation.
type EndImpersonationParams struct {
	ActorID  uuid.UUID
	TargetID uuid.UUID // the impersonated user pulled from the session
}

// EndImpersonation writes the closing audit bookend. The handler is
// responsible for the idempotent "no-op when not impersonating" case
// (it just doesn't call this method). When the handler does call it,
// we always write a row so the bookend pair is invariant.
func (s *Service) EndImpersonation(ctx context.Context, p EndImpersonationParams) error {
	return s.writeImpersonationBookend(ctx, p.ActorID, p.TargetID, ActionImpersonateEnded)
}

// writeImpersonationBookend is the audit-only path shared by Start /
// End. Wraps the action constant + metadata so the two callers stay
// in sync.
func (s *Service) writeImpersonationBookend(ctx context.Context, actor, target uuid.UUID, action string) error {
	auditID, err := uuid.NewV7()
	if err != nil {
		return apierror.Internal("admin: uuid").WithCause(err)
	}
	if err := s.audit.Create(ctx, auditrepo.CreateParams{
		ID: auditID, ActorID: &actor, Action: action,
		Metadata: map[string]any{MetadataImpersonatingUserID: target.String()},
	}); err != nil {
		return apierror.Internal(fmt.Sprintf("admin: write %s audit", action)).WithCause(err)
	}
	return nil
}

func ptrStr(s string) *string { return &s }
