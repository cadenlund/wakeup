// Package friend is the friendship service: SendRequest, AcceptRequest,
// DeclineRequest, ListFriends, ListRequests, Unfriend, Block, Unblock.
//
// Business rules (§4.6 + §16 Phase 4):
//
//   - A user cannot friend / block themselves.
//   - Send/Block resolve a target. Send takes a username (the §6.2 body
//     shape); Block takes a user_id (path param). Targets must exist
//     and not be soft-deleted.
//   - At most one row per pair (the pair-unique index in migration 0003
//     enforces it). Existing block in either direction prevents new
//     friend requests.
//   - Only the addressee can Accept/Decline a pending request. Either
//     side can Unfriend an accepted relationship. Only the blocker can
//     Unblock.
package friend

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// Service composes the friendship + user repositories. Goroutine-safe.
type Service struct {
	friends *friendrepo.Queries
	users   *userrepo.Queries
}

// Config builds the service.
type Config struct {
	Friends *friendrepo.Queries
	Users   *userrepo.Queries
}

// New constructs the service. Returns an error when any dependency is missing.
func New(cfg Config) (*Service, error) {
	if cfg.Friends == nil {
		return nil, errors.New("friend: Config.Friends is required")
	}
	if cfg.Users == nil {
		return nil, errors.New("friend: Config.Users is required")
	}
	return &Service{friends: cfg.Friends, users: cfg.Users}, nil
}

// SendRequest creates a pending friendship from `from` to the user with
// the given username. Returns the created row.
//
// Errors map to the §6.2 response codes:
//   - target not found / soft-deleted   → apierror.NotFound("user")
//   - target == self                    → apierror.Validation
//   - existing block in either direction → apierror.Conflict
//   - existing pending/accepted row     → apierror.Conflict
func (s *Service) SendRequest(ctx context.Context, from uuid.UUID, toUsername string) (domain.Friendship, error) {
	target, err := s.users.GetByUsername(ctx, toUsername)
	if err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.Friendship{}, apierror.NotFound("user")
		}
		return domain.Friendship{}, apierror.Internal("lookup target user").WithCause(err)
	}
	if target.ID == from {
		return domain.Friendship{}, apierror.Validation([]apierror.FieldError{{
			Field: "username", Code: "INVALID_VALUE",
			Message: "cannot friend yourself",
		}})
	}

	id, err := uuid.NewV7()
	if err != nil {
		return domain.Friendship{}, apierror.Internal("uuid").WithCause(err)
	}
	created, err := s.friends.Create(ctx, friendrepo.CreateParams{
		ID: id, RequesterID: from, AddresseeID: target.ID,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Existing row: either pending/accepted (already friends or
			// requested) OR blocked. Both surface as 409 — we don't
			// reveal which to avoid leaking who blocked whom.
			return domain.Friendship{}, apierror.Conflict("friendship already exists or has been blocked")
		}
		return domain.Friendship{}, apierror.Internal("create friend request").WithCause(err)
	}
	return created, nil
}

// AcceptRequest transitions a pending row to accepted.
//
// Errors:
//   - row missing                        → apierror.NotFound
//   - row already accepted/blocked       → apierror.Conflict
//   - actor is not the addressee         → apierror.Forbidden
func (s *Service) AcceptRequest(ctx context.Context, actor uuid.UUID, friendshipID uuid.UUID) (domain.Friendship, error) {
	f, err := s.friends.GetByID(ctx, friendshipID)
	if err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return domain.Friendship{}, apierror.NotFound("friend request")
		}
		return domain.Friendship{}, apierror.Internal("get friend request").WithCause(err)
	}
	if f.AddresseeID != actor {
		// Per §4.6 we use 403 here — the row exists but the caller
		// isn't allowed to act on it.
		return domain.Friendship{}, apierror.Forbidden("only the addressee can accept this request")
	}
	if f.Status != domain.FriendshipPending {
		return domain.Friendship{}, apierror.Conflict("friend request is not pending")
	}
	updated, err := s.friends.Accept(ctx, friendshipID)
	if err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			// Race: status changed between Get and Accept.
			return domain.Friendship{}, apierror.Conflict("friend request is not pending")
		}
		return domain.Friendship{}, apierror.Internal("accept friend request").WithCause(err)
	}
	return updated, nil
}

// DeclineRequest deletes a pending row. Only the addressee can decline
// — the requester cancels via a separate flow (out of scope for v1).
//
// Errors:
//   - row missing                  → apierror.NotFound
//   - row not pending              → apierror.Conflict
//   - actor is not the addressee   → apierror.Forbidden
func (s *Service) DeclineRequest(ctx context.Context, actor, friendshipID uuid.UUID) error {
	f, err := s.friends.GetByID(ctx, friendshipID)
	if err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return apierror.NotFound("friend request")
		}
		return apierror.Internal("get friend request").WithCause(err)
	}
	if f.AddresseeID != actor {
		return apierror.Forbidden("only the addressee can decline this request")
	}
	if f.Status != domain.FriendshipPending {
		return apierror.Conflict("friend request is not pending")
	}
	if err := s.friends.Delete(ctx, friendshipID); err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			// Already gone — idempotent.
			return nil
		}
		return apierror.Internal("delete friend request").WithCause(err)
	}
	return nil
}

// ListFriendsParams is the input to ListFriends.
type ListFriendsParams struct {
	UserID uuid.UUID
	Cursor *pagination.Cursor
	Limit  int
}

// ListFriendsResult is the paginated payload returned by ListFriends.
type ListFriendsResult struct {
	Friendships []domain.Friendship
	NextCursor  *string
	HasMore     bool
}

// ListFriends returns the user's accepted friendships, keyset-paginated.
func (s *Service) ListFriends(ctx context.Context, p ListFriendsParams) (ListFriendsResult, error) {
	rows, err := s.friends.ListAcceptedByUser(ctx, p.UserID, p.Cursor, p.Limit)
	if err != nil {
		return ListFriendsResult{}, apierror.Internal("list friends").WithCause(err)
	}
	data, next, hasMore := pagination.Page(rows, p.Limit, func(f domain.Friendship) pagination.Cursor {
		// AcceptedAt is non-nil for accepted rows by definition.
		ts := f.CreatedAt
		if f.AcceptedAt != nil {
			ts = *f.AcceptedAt
		}
		return pagination.Cursor{Timestamp: ts, ID: f.ID}
	})
	return ListFriendsResult{Friendships: data, NextCursor: next, HasMore: hasMore}, nil
}

// ListRequestsResult separates pending requests by direction so the
// frontend can render incoming and outgoing differently.
type ListRequestsResult struct {
	Incoming []domain.Friendship // user is the addressee
	Outgoing []domain.Friendship // user is the requester
}

// ListRequests returns pending friend requests partitioned by direction.
func (s *Service) ListRequests(ctx context.Context, userID uuid.UUID) (ListRequestsResult, error) {
	rows, err := s.friends.ListPendingByUser(ctx, userID)
	if err != nil {
		return ListRequestsResult{}, apierror.Internal("list friend requests").WithCause(err)
	}
	out := ListRequestsResult{}
	for _, f := range rows {
		if f.RequesterID == userID {
			out.Outgoing = append(out.Outgoing, f)
		} else {
			out.Incoming = append(out.Incoming, f)
		}
	}
	return out, nil
}

// Unfriend deletes an accepted friendship between actor and other.
// Either side can unfriend.
//
// Errors:
//   - no row between the pair        → apierror.NotFound
//   - row exists but not accepted    → apierror.Conflict (use Decline / Unblock instead)
func (s *Service) Unfriend(ctx context.Context, actor, other uuid.UUID) error {
	if actor == other {
		return apierror.Validation([]apierror.FieldError{{
			Field: "user_id", Code: "INVALID_VALUE",
			Message: "cannot unfriend yourself",
		}})
	}
	f, err := s.friends.GetByPair(ctx, actor, other)
	if err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return apierror.NotFound("friendship")
		}
		return apierror.Internal("get friendship").WithCause(err)
	}
	if f.Status != domain.FriendshipAccepted {
		return apierror.Conflict("friendship is not accepted")
	}
	if err := s.friends.Delete(ctx, f.ID); err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return nil // idempotent
		}
		return apierror.Internal("delete friendship").WithCause(err)
	}
	return nil
}

// Block records `actor` as having blocked `other`. The current row
// (pending / accepted / nothing) is replaced with a `blocked` row whose
// requester=actor — that's how we remember who initiated the block, so
// only they can Unblock later.
//
// Errors:
//   - actor == other                → apierror.Validation
//   - target user doesn't exist     → apierror.NotFound
//   - target already blocked us     → apierror.Forbidden (don't reveal)
func (s *Service) Block(ctx context.Context, actor, other uuid.UUID) (domain.Friendship, error) {
	if actor == other {
		return domain.Friendship{}, apierror.Validation([]apierror.FieldError{{
			Field: "user_id", Code: "INVALID_VALUE",
			Message: "cannot block yourself",
		}})
	}
	if _, err := s.users.GetByID(ctx, other); err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return domain.Friendship{}, apierror.NotFound("user")
		}
		return domain.Friendship{}, apierror.Internal("get target user").WithCause(err)
	}

	// If there's already a blocked row by the OTHER party, refuse — we
	// don't want to let A overwrite B's block via a re-block. The error
	// is intentionally generic (apierror.Forbidden, no leak about
	// "they blocked you").
	if existing, err := s.friends.GetByPair(ctx, actor, other); err == nil {
		if existing.IsBlocked() && existing.RequesterID != actor {
			return domain.Friendship{}, apierror.Forbidden("blocked")
		}
	} else if !errors.Is(err, friendrepo.ErrNotFound) {
		return domain.Friendship{}, apierror.Internal("get existing pair").WithCause(err)
	}

	// Best-effort: replace whatever's there with a fresh blocked row.
	// Two-step (delete then insert) is safe under the pair-unique
	// index because both operations are scoped to this single pair.
	if err := s.friends.DeleteByPair(ctx, actor, other); err != nil {
		return domain.Friendship{}, apierror.Internal("clear pair before block").WithCause(err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Friendship{}, apierror.Internal("uuid").WithCause(err)
	}
	created, err := s.friends.Create(ctx, friendrepo.CreateParams{
		ID: id, RequesterID: actor, AddresseeID: other,
		Status: domain.FriendshipBlocked,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Race: another writer landed a row between our delete and
			// insert. Surface as Conflict and let the client retry.
			return domain.Friendship{}, apierror.Conflict("block conflict — retry")
		}
		return domain.Friendship{}, apierror.Internal("create block row").WithCause(err)
	}
	return created, nil
}

// Unblock deletes the block row, leaving no friendship between the
// pair. Only the blocker (the row's requester) can call this — the
// other party can't, since they can't see the block exists.
func (s *Service) Unblock(ctx context.Context, actor, other uuid.UUID) error {
	if actor == other {
		return apierror.Validation([]apierror.FieldError{{
			Field: "user_id", Code: "INVALID_VALUE",
			Message: "cannot unblock yourself",
		}})
	}
	f, err := s.friends.GetByPair(ctx, actor, other)
	if err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return apierror.NotFound("block")
		}
		return apierror.Internal("get block").WithCause(err)
	}
	if !f.IsBlocked() || f.RequesterID != actor {
		// Either the row isn't blocked, or it's a block by the other
		// party — both render as not-found from the actor's POV.
		return apierror.NotFound("block")
	}
	if err := s.friends.Delete(ctx, f.ID); err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return nil
		}
		return apierror.Internal("delete block").WithCause(err)
	}
	return nil
}

// isUniqueViolation matches Postgres SQLSTATE 23505 — the pair-unique
// index trips this when two requests race to friend the same pair, or
// when an existing block prevents a new request.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
