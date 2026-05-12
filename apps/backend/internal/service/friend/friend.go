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
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// PresenceLister is the slice of presence.Service this package needs to
// gate the §11.5 friend-request push on a recipient with no live WS.
type PresenceLister interface {
	ListForUsers(ctx context.Context, ids []uuid.UUID) ([]domain.PresenceState, error)
}

// OfflinePusher is the slice of notification.Service this package needs.
// Same shape as notification.Service.SendOfflinePush. The trailing
// *uuid.UUID is the optional conversation scope — friend requests pass
// nil because they aren't conversation-scoped (no per-conv mute applies).
type OfflinePusher interface {
	SendOfflinePush(ctx context.Context, recipientID uuid.UUID, category notificationpref.Category, payload pushnotif.Notification, convID *uuid.UUID) error
}

// Service composes the friendship + user repositories. Goroutine-safe.
type Service struct {
	friends       *friendrepo.Queries
	users         *userrepo.Queries
	convs         *convrepo.Queries
	presence      PresenceLister
	notifications OfflinePusher
	broker        pubsub.Broker // optional — nil ⇒ in-app friend events aren't published
	logger        *slog.Logger
}

// Config builds the service. Presence + Notifications + Broker are
// optional — when nil they no-op (offline-push fan-out / in-app WS
// events respectively). Convs is required: Unfriend / Block both
// clear DMs between the pair, so the friend service needs the
// conversation repo to do the cascading delete.
type Config struct {
	Friends       *friendrepo.Queries
	Users         *userrepo.Queries
	Convs         *convrepo.Queries
	Presence      PresenceLister
	Notifications OfflinePusher
	Broker        pubsub.Broker
	Logger        *slog.Logger
}

// New constructs the service. Returns an error when any dependency is missing.
func New(cfg Config) (*Service, error) {
	if cfg.Friends == nil {
		return nil, errors.New("friend: Config.Friends is required")
	}
	if cfg.Users == nil {
		return nil, errors.New("friend: Config.Users is required")
	}
	if cfg.Convs == nil {
		return nil, errors.New("friend: Config.Convs is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		friends:       cfg.Friends,
		users:         cfg.Users,
		convs:         cfg.Convs,
		presence:      cfg.Presence,
		notifications: cfg.Notifications,
		broker:        cfg.Broker,
		logger:        logger,
	}, nil
}

// publishFriendEvent fires-and-forgets a `friend.request_*` envelope on
// `user:<recipientID>:events` so the recipient's open app surfaces an
// in-app notification. No-op when the broker isn't wired. `other` is
// the person the event is *about* — the requester for `request_received`,
// the accepter for `request_accepted`.
func (s *Service) publishFriendEvent(ctx context.Context, eventType wsproto.EventType, recipientID, requestID uuid.UUID, other domain.User) {
	if s.broker == nil {
		return
	}
	payload, err := wsproto.Encode(eventType, wsproto.FriendRequestEventPayload{
		RequestID: requestID,
		User:      wsproto.WSUser{ID: other.ID, Username: other.Username, DisplayName: other.DisplayName},
	})
	if err != nil {
		s.logger.Warn("friend: encode event", slog.String("error", err.Error()))
		return
	}
	channel := fmt.Sprintf("user:%s:events", recipientID)
	if err := s.broker.Publish(ctx, channel, payload); err != nil {
		s.logger.Warn("friend: publish event",
			slog.String("channel", channel),
			slog.String("error", err.Error()),
		)
	}
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
	s.maybePushFriendRequest(ctx, created)
	if requester, err := s.users.GetByID(ctx, from); err == nil {
		s.publishFriendEvent(ctx, wsproto.EventFriendRequestReceived, target.ID, created.ID, requester)
	} else {
		s.logger.Warn("friend: lookup requester for event", slog.String("error", err.Error()))
	}
	return created, nil
}

// maybePushFriendRequest sends an Expo push to the addressee when they
// don't have a live WS connection. Push errors are logged at warn level
// and never bubble up — push is a best-effort side channel and a
// transient outage mustn't undo a successful friend request.
func (s *Service) maybePushFriendRequest(ctx context.Context, f domain.Friendship) {
	if s.presence == nil || s.notifications == nil {
		return
	}
	presences, err := s.presence.ListForUsers(ctx, []uuid.UUID{f.AddresseeID})
	if err != nil {
		s.logger.Warn("friend: offline-push: list presence",
			slog.String("addressee_id", f.AddresseeID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	for _, ps := range presences {
		if ps.Status == domain.PresenceOnline || ps.Status == domain.PresenceAway {
			return
		}
	}
	payload := pushnotif.Notification{
		Title: "Friend request",
		Body:  "Someone sent you a friend request",
		Data: map[string]any{
			"type":          "friend_request",
			"friendship_id": f.ID.String(),
		},
	}
	if err := s.notifications.SendOfflinePush(ctx, f.AddresseeID, notificationpref.CategoryFriendRequests, payload, nil); err != nil {
		s.logger.Warn("friend: offline-push: send",
			slog.String("addressee_id", f.AddresseeID.String()),
			slog.String("error", err.Error()),
		)
	}
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
	// Tell the requester (the original sender) that you accepted.
	if accepter, err := s.users.GetByID(ctx, actor); err == nil {
		s.publishFriendEvent(ctx, wsproto.EventFriendRequestAccepted, f.RequesterID, updated.ID, accepter)
	} else {
		s.logger.Warn("friend: lookup accepter for event", slog.String("error", err.Error()))
	}
	return updated, nil
}

// CancelRequest deletes a pending row from the requester's side —
// "I changed my mind, take that friend request back." The addressee
// cancels via DeclineRequest, which lives below this. Splitting the
// two endpoints lets each side carry its own audit trail and gives
// the client a clear "you sent this" vs "they sent this" affordance
// without inferring the relationship from the actor.
//
// The repo's DeletePendingByRequester is one atomic SQL statement —
// status='pending' AND requester_id=actor are checked at row-lock
// time, so an in-flight Accept by the addressee can't slip a row
// out from under us between a read and a delete. ErrNotFound from
// the repo is the "no longer cancelable" case; we surface it as
// Conflict (the more specific 409 the client already handles for
// stale request states).
//
// Errors:
//   - row no longer cancelable (already accepted, declined, never
//     existed, or owned by a different requester) → apierror.Conflict
func (s *Service) CancelRequest(ctx context.Context, actor, friendshipID uuid.UUID) error {
	if err := s.friends.DeletePendingByRequester(ctx, friendshipID, actor); err != nil {
		if errors.Is(err, friendrepo.ErrNotFound) {
			return apierror.Conflict("friend request is not pending or not owned by you")
		}
		return apierror.Internal("cancel friend request").WithCause(err)
	}
	return nil
}

// DeclineRequest deletes a pending row. Only the addressee can decline
// — the requester cancels via CancelRequest above.
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
// Total is the absolute friend count across every page, so the UI can
// render "showing N of M" hints without paging through every cursor.
type ListFriendsResult struct {
	Friendships []domain.Friendship
	Total       int
	NextCursor  *string
	HasMore     bool
}

// ListFriends returns the user's accepted friendships, keyset-paginated.
func (s *Service) ListFriends(ctx context.Context, p ListFriendsParams) (ListFriendsResult, error) {
	rows, err := s.friends.ListAcceptedByUser(ctx, p.UserID, p.Cursor, p.Limit)
	if err != nil {
		return ListFriendsResult{}, apierror.Internal("list friends").WithCause(err)
	}
	total, err := s.friends.CountAcceptedByUser(ctx, p.UserID)
	if err != nil {
		return ListFriendsResult{}, apierror.Internal("count friends").WithCause(err)
	}
	data, next, hasMore := pagination.Page(rows, p.Limit, func(f domain.Friendship) pagination.Cursor {
		// AcceptedAt is non-nil for accepted rows by definition.
		ts := f.CreatedAt
		if f.AcceptedAt != nil {
			ts = *f.AcceptedAt
		}
		return pagination.Cursor{Timestamp: ts, ID: f.ID}
	})
	return ListFriendsResult{Friendships: data, Total: total, NextCursor: next, HasMore: hasMore}, nil
}

// ListAcceptedFriendIDs returns every accepted friend's user_id,
// unpaginated. Used by the §9 presence service to fan out
// presence.update events to friends only — the §7.2 contract that
// presence is friends-only requires the full friend list at every
// publish. The graph is bounded by user behavior (realistic max in
// the hundreds), so unpaginated is fine for v1.
func (s *Service) ListAcceptedFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.friends.ListAllAcceptedFriendIDs(ctx, userID)
	if err != nil {
		return nil, apierror.Internal("list accepted friend ids").WithCause(err)
	}
	return ids, nil
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

// ListBlocked returns the user_ids the caller has blocked. Only the
// blocker sees their own block list (addressees are unaware), so this
// returns just the addressee_id of every friendships row where the
// caller is the requester and status='blocked'.
func (s *Service) ListBlocked(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.friends.ListBlockedByUser(ctx, userID)
	if err != nil {
		return nil, apierror.Internal("list blocked friends").WithCause(err)
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, f := range rows {
		out = append(out, f.AddresseeID)
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
	// Clear any direct conversations between the pair — once the
	// friendship is gone the DM thread is no longer reachable per
	// the §1 friend-graph rule, and the product convention is to
	// drop the history rather than leave an orphan thread visible
	// in either user's chats list. Group conversations the pair
	// shares are intentionally NOT touched.
	if err := s.convs.DeleteDirectByPair(ctx, actor, other); err != nil {
		return apierror.Internal("clear direct conversations").WithCause(err)
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
	// Same DM-clearing rule as Unfriend — block drops every direct
	// conversation between the pair. Groups they share are kept;
	// per product policy the user can leave those manually if they
	// want to.
	if err := s.convs.DeleteDirectByPair(ctx, actor, other); err != nil {
		return domain.Friendship{}, apierror.Internal("clear direct conversations on block").WithCause(err)
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
