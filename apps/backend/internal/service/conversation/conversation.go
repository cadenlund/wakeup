// Package conversation is the service layer for conversation +
// conversation_members operations. Composes the conversation repo
// (§5.1) and the user repo behind apierror-typed methods.
//
// Business rules (§4.6 + §6.2):
//
//   - Direct conversations have exactly 2 members; groups have 2-25.
//   - Group creator gets `admin` role; everyone else is `member`.
//   - Direct dedup: creating a direct between users that already have
//     one returns the existing conversation.
//   - Update / AddMembers / RemoveMember (others) are admin-only on
//     groups. RemoveMember (self) == Leave.
//   - Leave on a direct removes the caller's membership — the other
//     party still sees the conversation.
//   - Member candidates must exist + not be soft-deleted.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// MinGroupMembers is the §4.6 minimum (2 — including the creator). 1-person
// groups are nonsense; 1-person directs are caught earlier as "self friend."
const MinGroupMembers = 2

// MaxNameLen is the §4.6 cap on group name. The handler validator also
// enforces this; the service double-checks for non-handler callers.
const MaxNameLen = 80

// Service is the conversation service.
type Service struct {
	pool    *pgxpool.Pool // for AddMemberWithCap and tx-wrapped creates
	convs   *convrepo.Queries
	users   *userrepo.Queries
	friends *friendrepo.Queries
}

// Config builds the service.
type Config struct {
	Pool    *pgxpool.Pool
	Convs   *convrepo.Queries
	Users   *userrepo.Queries
	Friends *friendrepo.Queries
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Pool == nil {
		return nil, errors.New("conversation: Config.Pool is required")
	}
	if cfg.Convs == nil {
		return nil, errors.New("conversation: Config.Convs is required")
	}
	if cfg.Users == nil {
		return nil, errors.New("conversation: Config.Users is required")
	}
	if cfg.Friends == nil {
		return nil, errors.New("conversation: Config.Friends is required")
	}
	return &Service{pool: cfg.Pool, convs: cfg.Convs, users: cfg.Users, friends: cfg.Friends}, nil
}

// CreateParams is the input to Create. Type=direct: MemberIDs MUST hold
// exactly one other user (Creator is implicit). Type=group: MemberIDs
// must be 1-24 unique others (Creator + len(MemberIDs) ≤ 25), and Name
// is required.
type CreateParams struct {
	Type      domain.ConversationType
	Creator   uuid.UUID
	MemberIDs []uuid.UUID
	Name      *string
	AvatarURL *string
}

// CreateResult is the (conversation, members) pair returned on success.
type CreateResult struct {
	Conversation domain.Conversation
	Members      []domain.ConversationMember
}

// Create handles both direct and group creation. For direct, deduplicates
// by checking if a direct conversation between the same pair already
// exists and returning it. For group, enforces name + cap-25.
//
// Wraps every insert in a single transaction so a partial failure
// (e.g. a member ID that fails the FK) doesn't leave a half-created
// conversation behind.
func (s *Service) Create(ctx context.Context, p CreateParams) (CreateResult, error) {
	switch p.Type {
	case domain.ConversationDirect:
		return s.createDirect(ctx, p)
	case domain.ConversationGroup:
		return s.createGroup(ctx, p)
	default:
		return CreateResult{}, apierror.Validation([]apierror.FieldError{{
			Field: "type", Code: "INVALID_VALUE",
			Message: "type must be 'direct' or 'group'",
		}})
	}
}

func (s *Service) createDirect(ctx context.Context, p CreateParams) (CreateResult, error) {
	if len(p.MemberIDs) != 1 {
		return CreateResult{}, apierror.Validation([]apierror.FieldError{{
			Field: "member_ids", Code: "INVALID_VALUE",
			Message: "direct conversations require exactly one other member",
		}})
	}
	other := p.MemberIDs[0]
	if other == p.Creator {
		return CreateResult{}, apierror.Validation([]apierror.FieldError{{
			Field: "member_ids", Code: "INVALID_VALUE",
			Message: "cannot create a direct conversation with yourself",
		}})
	}
	if _, err := s.users.GetByID(ctx, other); err != nil {
		if errors.Is(err, userrepo.ErrNotFound) {
			return CreateResult{}, apierror.NotFound("user")
		}
		return CreateResult{}, apierror.Internal("lookup other user").WithCause(err)
	}

	// Dedup: if a direct already exists, return it. Existing DMs
	// short-circuit the friendship check below — once a thread is
	// open, an unfriend shouldn't lock you out of re-reading the
	// history. Creating a new DM is what requires friendship.
	if existing, err := s.convs.GetDirectByPair(ctx, p.Creator, other); err == nil {
		members, err := s.convs.ListMembers(ctx, existing.ID)
		if err != nil {
			return CreateResult{}, apierror.Internal("load members").WithCause(err)
		}
		return CreateResult{Conversation: existing, Members: members}, nil
	} else if !errors.Is(err, convrepo.ErrNotFound) {
		return CreateResult{}, apierror.Internal("dedup direct").WithCause(err)
	}

	// Friends-only DM enforcement (§1 product overview: "friend-graph
	// chat"). Without this, anyone you can search by username can be
	// DM'd, which sidesteps the friend-request consent step. The
	// frontend gates the affordance too, but the server is the
	// authority — a direct CALL to POST /v1/conversations with
	// non-friend ids must 403, not silently create a chat.
	pair, err := s.friends.GetByPair(ctx, p.Creator, other)
	if err != nil && !errors.Is(err, friendrepo.ErrNotFound) {
		return CreateResult{}, apierror.Internal("check friendship").WithCause(err)
	}
	if errors.Is(err, friendrepo.ErrNotFound) || pair.Status != domain.FriendshipAccepted {
		return CreateResult{}, apierror.Forbidden("you must be friends to start a conversation")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, apierror.Internal("begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.convs.WithTx(tx)

	id, err := uuid.NewV7()
	if err != nil {
		return CreateResult{}, apierror.Internal("uuid").WithCause(err)
	}
	conv, err := q.CreateConversation(ctx, convrepo.CreateParams{
		ID: id, Type: domain.ConversationDirect, CreatedBy: p.Creator,
	})
	if err != nil {
		return CreateResult{}, apierror.Internal("create conversation").WithCause(err)
	}
	creatorMember, err := q.AddMember(ctx, conv.ID, p.Creator, domain.MemberRoleMember)
	if err != nil {
		return CreateResult{}, apierror.Internal("add creator member").WithCause(err)
	}
	otherMember, err := q.AddMember(ctx, conv.ID, other, domain.MemberRoleMember)
	if err != nil {
		// Race: someone else created a direct between the same pair
		// between our dedup check and now. Surface as Conflict so the
		// client retries (and gets the dedup'd row on the retry).
		if isUniqueViolation(err) {
			return CreateResult{}, apierror.Conflict("direct conversation already exists — retry")
		}
		return CreateResult{}, apierror.Internal("add other member").WithCause(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, apierror.Internal("commit").WithCause(err)
	}
	return CreateResult{
		Conversation: conv,
		Members:      []domain.ConversationMember{creatorMember, otherMember},
	}, nil
}

func (s *Service) createGroup(ctx context.Context, p CreateParams) (CreateResult, error) {
	if err := validateGroupName(p.Name, false); err != nil {
		return CreateResult{}, err
	}

	// Dedupe member IDs and exclude the creator (auto-included).
	uniqueOthers := make(map[uuid.UUID]struct{}, len(p.MemberIDs))
	for _, id := range p.MemberIDs {
		if id == p.Creator {
			continue
		}
		uniqueOthers[id] = struct{}{}
	}
	others := make([]uuid.UUID, 0, len(uniqueOthers))
	for id := range uniqueOthers {
		others = append(others, id)
	}

	total := 1 + len(others) // creator + invitees
	if total < MinGroupMembers {
		return CreateResult{}, apierror.Validation([]apierror.FieldError{{
			Field: "member_ids", Code: "INVALID_VALUE",
			Message: fmt.Sprintf("group conversations require at least %d members", MinGroupMembers),
		}})
	}
	if total > convrepo.MaxGroupMembers {
		return CreateResult{}, apierror.Validation([]apierror.FieldError{{
			Field: "member_ids", Code: "INVALID_VALUE",
			Message: fmt.Sprintf("group conversations are capped at %d members", convrepo.MaxGroupMembers),
		}})
	}

	// Validate every other-member exists + isn't soft-deleted.
	if len(others) > 0 {
		users, err := s.users.ListByIDs(ctx, others)
		if err != nil {
			return CreateResult{}, apierror.Internal("validate members").WithCause(err)
		}
		alive := make(map[uuid.UUID]struct{}, len(users))
		for _, u := range users {
			if u.DeletedAt == nil {
				alive[u.ID] = struct{}{}
			}
		}
		for _, id := range others {
			if _, ok := alive[id]; !ok {
				return CreateResult{}, apierror.NotFound("user")
			}
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, apierror.Internal("begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.convs.WithTx(tx)

	id, err := uuid.NewV7()
	if err != nil {
		return CreateResult{}, apierror.Internal("uuid").WithCause(err)
	}
	conv, err := q.CreateConversation(ctx, convrepo.CreateParams{
		ID: id, Type: domain.ConversationGroup, Name: p.Name, AvatarURL: p.AvatarURL, CreatedBy: p.Creator,
	})
	if err != nil {
		return CreateResult{}, apierror.Internal("create conversation").WithCause(err)
	}

	members := make([]domain.ConversationMember, 0, total)
	creatorMember, err := q.AddMember(ctx, conv.ID, p.Creator, domain.MemberRoleAdmin)
	if err != nil {
		return CreateResult{}, apierror.Internal("add creator member").WithCause(err)
	}
	members = append(members, creatorMember)
	for _, otherID := range others {
		m, err := q.AddMember(ctx, conv.ID, otherID, domain.MemberRoleMember)
		if err != nil {
			if isUniqueViolation(err) {
				// Duplicate ID slipped past dedupe — caller bug.
				return CreateResult{}, apierror.Validation([]apierror.FieldError{{
					Field: "member_ids", Code: "INVALID_VALUE",
					Message: "member_ids contains duplicates",
				}})
			}
			return CreateResult{}, apierror.Internal("add member").WithCause(err)
		}
		members = append(members, m)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, apierror.Internal("commit").WithCause(err)
	}
	return CreateResult{Conversation: conv, Members: members}, nil
}

// GetResult is the conversation + its members, returned by Get.
type GetResult struct {
	Conversation domain.Conversation
	Members      []domain.ConversationMember
}

// Get returns the conversation and its full member list. Returns
// apierror.NotFound when the conversation doesn't exist OR the caller
// isn't a member (avoids leaking conversation existence to non-members).
func (s *Service) Get(ctx context.Context, actor, convID uuid.UUID) (GetResult, error) {
	if _, err := s.convs.GetMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return GetResult{}, apierror.NotFound("conversation")
		}
		return GetResult{}, apierror.Internal("get member").WithCause(err)
	}
	conv, err := s.convs.GetConversation(ctx, convID)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return GetResult{}, apierror.NotFound("conversation")
		}
		return GetResult{}, apierror.Internal("get conversation").WithCause(err)
	}
	members, err := s.convs.ListMembers(ctx, convID)
	if err != nil {
		return GetResult{}, apierror.Internal("list members").WithCause(err)
	}
	return GetResult{Conversation: conv, Members: members}, nil
}

// ListParams is the input to List.
type ListParams struct {
	UserID uuid.UUID
	Cursor *pagination.Cursor
	Limit  int
}

// ListResult is the paginated payload returned by List.
type ListResult struct {
	Conversations []domain.Conversation
	NextCursor    *string
	HasMore       bool
}

// ListMembersForConversations batch-loads members for a slice of
// conversation IDs. The handler uses this to render a paginated list
// without firing N+1 queries — one round-trip per page instead of one
// per row (CodeRabbit caught the naive renderConversationList loop on
// PR #36).
//
// No membership check here — callers MUST have already verified the
// requesting user is a member of every id (e.g. by passing only the
// IDs returned from List). Surfacing rows for non-member conversations
// would break the §4.6 enumeration invariant.
func (s *Service) ListMembersForConversations(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]domain.ConversationMember, error) {
	out, err := s.convs.ListMembersForConversations(ctx, ids)
	if err != nil {
		return nil, apierror.Internal("list members for conversations").WithCause(err)
	}
	return out, nil
}

// List returns the user's conversations keyset-paginated by
// (last_message_at DESC, id DESC).
func (s *Service) List(ctx context.Context, p ListParams) (ListResult, error) {
	rows, err := s.convs.ListConversationsByUser(ctx, p.UserID, p.Cursor, p.Limit)
	if err != nil {
		return ListResult{}, apierror.Internal("list conversations").WithCause(err)
	}
	data, next, hasMore := pagination.Page(rows, p.Limit, func(c domain.Conversation) pagination.Cursor {
		return pagination.Cursor{Timestamp: c.LastMessageAt, ID: c.ID}
	})
	return ListResult{Conversations: data, NextCursor: next, HasMore: hasMore}, nil
}

// UpdateParams is the input to Update. Only group conversations can be
// updated; both Name and AvatarURL are optional.
type UpdateParams struct {
	Actor     uuid.UUID
	ConvID    uuid.UUID
	Name      *string
	AvatarURL *string
}

// Update patches a group conversation's name / avatar_url. The caller
// must be an admin of the conversation. Direct conversations are
// immutable — Update returns Forbidden for them, but only AFTER
// confirming membership so non-members can't enumerate which
// conversations exist (CodeRabbit caught the leak on PR #35).
func (s *Service) Update(ctx context.Context, p UpdateParams) (domain.Conversation, error) {
	// Check membership first; non-members get NotFound regardless of
	// the conversation's type.
	member, err := s.convs.GetMember(ctx, p.ConvID, p.Actor)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.Conversation{}, apierror.NotFound("conversation")
		}
		return domain.Conversation{}, apierror.Internal("get member").WithCause(err)
	}
	conv, err := s.convs.GetConversation(ctx, p.ConvID)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.Conversation{}, apierror.NotFound("conversation")
		}
		return domain.Conversation{}, apierror.Internal("get conversation").WithCause(err)
	}
	if !conv.IsGroup() {
		return domain.Conversation{}, apierror.Forbidden("only group conversations are mutable")
	}
	if !member.IsAdmin() {
		return domain.Conversation{}, apierror.Forbidden("only group admins can update the conversation")
	}
	if err := validateGroupName(p.Name, true); err != nil {
		return domain.Conversation{}, err
	}
	updated, err := s.convs.UpdateConversation(ctx, convrepo.UpdateParams{
		ID: p.ConvID, Name: p.Name, AvatarURL: p.AvatarURL,
	})
	if err != nil {
		return domain.Conversation{}, apierror.Internal("update conversation").WithCause(err)
	}
	return updated, nil
}

// SetMute toggles the per-member push-suppression deadline for a
// conversation. mutedUntil = nil unmutes; a future timestamp suppresses
// pushes until then; a far-future stamp ('2099-01-01') is the canonical
// "forever" value (handler builds it from the bool the client sends).
//
// Membership-gated: non-members get NotFound to avoid enumeration.
func (s *Service) SetMute(ctx context.Context, actor, convID uuid.UUID, mutedUntil *time.Time) (domain.ConversationMember, error) {
	if _, err := s.convs.GetMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.ConversationMember{}, apierror.NotFound("conversation")
		}
		return domain.ConversationMember{}, apierror.Internal("get member").WithCause(err)
	}
	updated, err := s.convs.SetMute(ctx, convID, actor, mutedUntil)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.ConversationMember{}, apierror.NotFound("conversation")
		}
		return domain.ConversationMember{}, apierror.Internal("set mute").WithCause(err)
	}
	return updated, nil
}

// SetPin toggles the per-member pin marker. The handler passes a
// boolean — the service stamps `now()` on pin and clears on unpin so
// the timestamp policy stays in one place (CodeRabbit on PR #101 —
// the original handler-side timestamp generation blurred the
// handler→service boundary).
func (s *Service) SetPin(ctx context.Context, actor, convID uuid.UUID, pinned bool) (domain.ConversationMember, error) {
	if _, err := s.convs.GetMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.ConversationMember{}, apierror.NotFound("conversation")
		}
		return domain.ConversationMember{}, apierror.Internal("get member").WithCause(err)
	}
	var pinnedAt *time.Time
	if pinned {
		now := time.Now()
		pinnedAt = &now
	}
	updated, err := s.convs.SetPin(ctx, convID, actor, pinnedAt)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return domain.ConversationMember{}, apierror.NotFound("conversation")
		}
		return domain.ConversationMember{}, apierror.Internal("set pin").WithCause(err)
	}
	return updated, nil
}

// Leave removes the caller from a conversation.
//
// For groups this is straightforward — the caller's membership row is
// deleted. For directs the spec describes "hide" but our schema doesn't
// have a hidden flag; v1 implements hide as a removal, which is
// unobservable to the other party (their view of the conversation is
// unaffected — they still see message history).
func (s *Service) Leave(ctx context.Context, actor, convID uuid.UUID) error {
	if _, err := s.convs.GetMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("conversation")
		}
		return apierror.Internal("get member").WithCause(err)
	}
	if err := s.convs.RemoveMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return nil // already removed; idempotent
		}
		return apierror.Internal("remove member").WithCause(err)
	}
	return nil
}

// AddMembersParams is the input to AddMembers.
type AddMembersParams struct {
	Actor   uuid.UUID
	ConvID  uuid.UUID
	UserIDs []uuid.UUID
}

// AddMembersResult is the payload returned by AddMembers.
type AddMembersResult struct {
	Added []domain.ConversationMember
}

// AddMembers adds users to a group conversation. Caller must be an
// admin. Each invitee is added with role=member and goes through
// AddMemberWithCap so the cap-25 invariant is preserved under
// concurrent adds.
//
// Membership is checked before any conversation-type / role checks so
// non-members can't enumerate which conversation IDs exist.
func (s *Service) AddMembers(ctx context.Context, p AddMembersParams) (AddMembersResult, error) {
	caller, err := s.convs.GetMember(ctx, p.ConvID, p.Actor)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return AddMembersResult{}, apierror.NotFound("conversation")
		}
		return AddMembersResult{}, apierror.Internal("get member").WithCause(err)
	}
	conv, err := s.convs.GetConversation(ctx, p.ConvID)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return AddMembersResult{}, apierror.NotFound("conversation")
		}
		return AddMembersResult{}, apierror.Internal("get conversation").WithCause(err)
	}
	if !conv.IsGroup() {
		return AddMembersResult{}, apierror.Forbidden("cannot add members to a direct conversation")
	}
	if !caller.IsAdmin() {
		return AddMembersResult{}, apierror.Forbidden("only group admins can add members")
	}

	// Dedup the request, drop the actor (already in), and drop anyone
	// who's already a member. Doing this up front avoids extra DB
	// work in the loop below and keeps the cap-25 check honest
	// (CodeRabbit caught the loop-only filter on PR #35).
	existingMembers, err := s.convs.ListMembers(ctx, p.ConvID)
	if err != nil {
		return AddMembersResult{}, apierror.Internal("list members").WithCause(err)
	}
	existing := make(map[uuid.UUID]struct{}, len(existingMembers))
	for _, m := range existingMembers {
		existing[m.UserID] = struct{}{}
	}

	unique := make(map[uuid.UUID]struct{}, len(p.UserIDs))
	for _, id := range p.UserIDs {
		if _, already := existing[id]; already {
			continue
		}
		unique[id] = struct{}{}
	}
	delete(unique, p.Actor) // belt and braces — actor is in `existing` too
	candidates := make([]uuid.UUID, 0, len(unique))
	for id := range unique {
		candidates = append(candidates, id)
	}
	if len(candidates) == 0 {
		return AddMembersResult{Added: nil}, nil
	}

	// Validate each candidate exists + isn't soft-deleted.
	users, err := s.users.ListByIDs(ctx, candidates)
	if err != nil {
		return AddMembersResult{}, apierror.Internal("validate users").WithCause(err)
	}
	alive := make(map[uuid.UUID]struct{}, len(users))
	for _, u := range users {
		if u.DeletedAt == nil {
			alive[u.ID] = struct{}{}
		}
	}
	for _, id := range candidates {
		if _, ok := alive[id]; !ok {
			return AddMembersResult{}, apierror.NotFound("user")
		}
	}

	added := make([]domain.ConversationMember, 0, len(candidates))
	for _, id := range candidates {
		m, err := convrepo.AddMemberWithCap(ctx, s.pool, p.ConvID, id, domain.MemberRoleMember, convrepo.MaxGroupMembers)
		if err != nil {
			if errors.Is(err, convrepo.ErrGroupTooLarge) {
				return AddMembersResult{Added: added}, apierror.Conflict(
					fmt.Sprintf("group is at the %d-member cap", convrepo.MaxGroupMembers))
			}
			if isUniqueViolation(err) {
				// User already a member from a concurrent add — skip.
				continue
			}
			return AddMembersResult{Added: added}, apierror.Internal("add member").WithCause(err)
		}
		added = append(added, m)
	}
	return AddMembersResult{Added: added}, nil
}

// RemoveMember removes a member from a conversation. Either:
//   - actor == target (self-removal == Leave), OR
//   - actor is an admin of a group (kicking another user)
//
// Direct conversations only support self-removal. Membership is checked
// first so non-members can't enumerate conversation IDs.
func (s *Service) RemoveMember(ctx context.Context, actor, convID, target uuid.UUID) error {
	if actor == target {
		return s.Leave(ctx, actor, convID)
	}
	caller, err := s.convs.GetMember(ctx, convID, actor)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("conversation")
		}
		return apierror.Internal("get member").WithCause(err)
	}
	conv, err := s.convs.GetConversation(ctx, convID)
	if err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("conversation")
		}
		return apierror.Internal("get conversation").WithCause(err)
	}
	if !conv.IsGroup() {
		return apierror.Forbidden("cannot remove other members from a direct conversation")
	}
	if !caller.IsAdmin() {
		return apierror.Forbidden("only group admins can remove other members")
	}
	if _, err := s.convs.GetMember(ctx, convID, target); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("member")
		}
		return apierror.Internal("get target member").WithCause(err)
	}
	if err := s.convs.RemoveMember(ctx, convID, target); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return nil // race; idempotent
		}
		return apierror.Internal("remove member").WithCause(err)
	}
	return nil
}

// MarkRead stamps the caller's last_read_message_id pointer. Caller
// must be a member of the conversation. Doesn't validate that the
// message belongs to the conversation — that's the message service's
// job at write time and would require a join here.
func (s *Service) MarkRead(ctx context.Context, actor, convID, messageID uuid.UUID) error {
	if _, err := s.convs.GetMember(ctx, convID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("conversation")
		}
		return apierror.Internal("get member").WithCause(err)
	}
	if err := s.convs.UpdateLastReadMessage(ctx, convID, actor, messageID); err != nil {
		return apierror.Internal("update last read").WithCause(err)
	}
	return nil
}

// validateGroupName checks the §4.6 rules: name must be 1-MaxNameLen
// runes (not bytes — `len` would reject valid non-ASCII names early).
//
//   - allowNil=false (Create): nil name is rejected as REQUIRED.
//   - allowNil=true  (Update): nil name means "don't touch", but a
//     non-nil pointer to "" is still REQUIRED — Update can't blank
//     a group's name.
func validateGroupName(name *string, allowNil bool) error {
	if name == nil {
		if allowNil {
			return nil
		}
		return apierror.Validation([]apierror.FieldError{{
			Field: "name", Code: "REQUIRED",
			Message: "group conversations require a name",
		}})
	}
	if *name == "" {
		return apierror.Validation([]apierror.FieldError{{
			Field: "name", Code: "REQUIRED",
			Message: "group conversations require a non-empty name",
		}})
	}
	if utf8.RuneCountInString(*name) > MaxNameLen {
		return apierror.Validation([]apierror.FieldError{{
			Field: "name", Code: "TOO_LONG",
			Message: fmt.Sprintf("name must be at most %d characters", MaxNameLen),
		}})
	}
	return nil
}

// isUniqueViolation matches Postgres SQLSTATE 23505. Used to detect
// race-condition member-already-exists scenarios.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
