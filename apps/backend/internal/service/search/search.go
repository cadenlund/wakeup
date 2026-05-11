// Package search composes the user / conversation / message repos
// into the §6.2 GET /v1/search unified-search endpoint. Each section
// (users, conversations, messages) is capped per request — the
// mobile global-search modal renders fast, paginated drill-downs are
// the per-section endpoints (/v1/users, /v1/conversations,
// /v1/conversations/{id}/messages).
package search

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// PerTypeLimit caps each section of the unified-search response.
// Mobile's §5.1 search modal renders 10 friends, 10 group chats, 10
// messages — anything more is a drill-down via the dedicated list
// endpoints, not this one.
const PerTypeLimit = 10

// MinQueryLen guards against single-char queries that would scan most
// of the database. The handler returns 422 for shorter input.
const MinQueryLen = 2

// Type is one of the section identifiers a caller can opt in/out of via
// the `types` query param. An empty Types slice means "all sections".
type Type string

// Section identifiers. Match the mobile spec's §5.1 wire shape exactly.
const (
	TypeUsers         Type = "users"
	TypeConversations Type = "conversations"
	TypeMessages      Type = "messages"
)

// allTypes is the default search set (every section).
var allTypes = []Type{TypeUsers, TypeConversations, TypeMessages}

// Service is the unified-search service.
type Service struct {
	users *usersvc.Service
	convs *convrepo.Queries
	msgs  *msgrepo.Queries
}

// Config builds the service.
type Config struct {
	Users *usersvc.Service
	Convs *convrepo.Queries
	Msgs  *msgrepo.Queries
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Users == nil {
		return nil, errors.New("search: Config.Users is required")
	}
	if cfg.Convs == nil {
		return nil, errors.New("search: Config.Convs is required")
	}
	if cfg.Msgs == nil {
		return nil, errors.New("search: Config.Msgs is required")
	}
	return &Service{users: cfg.Users, convs: cfg.Convs, msgs: cfg.Msgs}, nil
}

// Result is the §6.2 unified-search payload. Sections the caller didn't
// opt into (via the `types` query param) come back as nil. The
// `*Total` fields are the absolute population counts across every
// page — the slices are capped at PerTypeLimit (10) so the UI can
// render "showing 10 of 1000" hints and offer a drill-down.
type Result struct {
	Users              []domain.User
	UsersTotal         int
	Conversations      []domain.Conversation
	ConversationsTotal int
	Messages           []domain.Message
	MessagesTotal      int
}

// Params is the input to Search.
type Params struct {
	UserID uuid.UUID
	// Query is the user-supplied search string; the service trims it.
	// Empty / sub-MinQueryLen surfaces as Validation per §4.4.
	Query string
	// Types caps the search sections. nil/empty = all.
	Types []Type
}

// Search runs the unified search. Each requested section is fetched
// with its own repo call; the first failure aborts and propagates.
// Partial results aren't worth the complexity for a search box where
// the user just retries — the value of failing fast is the caller
// sees the underlying error instead of a quietly empty section.
func (s *Service) Search(ctx context.Context, p Params) (Result, error) {
	q := strings.TrimSpace(p.Query)
	if len(q) < MinQueryLen {
		return Result{}, apierror.Validation([]apierror.FieldError{{
			Field: "q", Code: "TOO_SHORT",
			Message: "q must be at least 2 characters",
		}})
	}
	types := p.Types
	if len(types) == 0 {
		types = allTypes
	}

	res := Result{}
	for _, t := range types {
		switch t {
		case TypeUsers:
			users, err := s.users.Search(ctx, usersvc.SearchParams{
				Query: q, Limit: PerTypeLimit, CallerID: &p.UserID,
			})
			if err != nil {
				return Result{}, err
			}
			res.Users = users.Users
			res.UsersTotal = users.Total
		case TypeConversations:
			convs, err := s.convs.SearchByUserAndName(ctx, p.UserID, q, PerTypeLimit)
			if err != nil {
				return Result{}, apierror.Internal("search conversations").WithCause(err)
			}
			res.Conversations = convs
			total, err := s.convs.CountSearchByUserAndName(ctx, p.UserID, q)
			if err != nil {
				return Result{}, apierror.Internal("count search conversations").WithCause(err)
			}
			res.ConversationsTotal = total
		case TypeMessages:
			msgs, err := s.msgs.SearchInUserConversations(ctx, p.UserID, q, PerTypeLimit)
			if err != nil {
				return Result{}, apierror.Internal("search messages").WithCause(err)
			}
			res.Messages = msgs
			total, err := s.msgs.CountSearchInUserConversations(ctx, p.UserID, q)
			if err != nil {
				return Result{}, apierror.Internal("count search messages").WithCause(err)
			}
			res.MessagesTotal = total
		}
	}
	return res, nil
}

// ParseTypes turns a comma-joined `types` query param into a typed
// slice. Empty / whitespace-only string returns nil (= "all sections"
// per Search's contract). Unknown tokens surface as Validation.
func ParseTypes(raw string) ([]Type, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]Type, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		t := Type(p)
		switch t {
		case TypeUsers, TypeConversations, TypeMessages:
			out = append(out, t)
		default:
			return nil, apierror.Validation([]apierror.FieldError{{
				Field: "types", Code: "INVALID_VALUE",
				Message: "types must be a comma-joined subset of: users, conversations, messages",
			}})
		}
	}
	return out, nil
}
