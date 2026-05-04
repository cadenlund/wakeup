package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	searchsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/search"
)

// SearchHandler hosts GET /v1/search.
type SearchHandler struct {
	search *searchsvc.Service
	auth   *auth.Service
	v      *validator.Validate
}

// NewSearchHandler wires up the handler.
func NewSearchHandler(s *searchsvc.Service, a *auth.Service, v *validator.Validate) (*SearchHandler, error) {
	if s == nil {
		return nil, errors.New("httpapi: SearchHandler requires non-nil search service")
	}
	if a == nil {
		return nil, errors.New("httpapi: SearchHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: SearchHandler requires non-nil validator")
	}
	return &SearchHandler{search: s, auth: a, v: v}, nil
}

// Mount attaches /v1/search onto r.
func (h *SearchHandler) Mount(r chi.Router) {
	r.Get("/v1/search", h.Search)
}

// SearchResponse is the wire shape for GET /v1/search.
//
// We use POINTER slices so the JSON encoder can distinguish three
// states cleanly:
//
//   - field omitted: the caller didn't request that section via
//     `types`. Pointer is nil → omitempty drops the key.
//   - empty array `[]`: the caller requested the section but got
//     zero matches. Pointer is non-nil but the slice is empty.
//   - populated array: hits.
//
// Plain `[]Type` with omitempty would conflate the first two states —
// an empty array would be indistinguishable from "not requested," and
// the mobile UI couldn't tell whether to render "no matches" vs hide
// the section entirely (CodeRabbit on PR #107).
type SearchResponse struct {
	Users         *[]UserResponse          `json:"users,omitempty"`
	Conversations *[]SearchConversationRow `json:"conversations,omitempty"`
	Messages      *[]SearchMessageRow      `json:"messages,omitempty"`
}

// SearchConversationRow is the slim conversation shape for unified
// search — full ConversationResponse would require a follow-up
// member-batch lookup, which the search modal doesn't need (the
// drill-down GET /v1/conversations/{id} provides the full row).
type SearchConversationRow struct {
	ID            string `json:"id"             example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Type          string `json:"type"           example:"group"`
	Name          string `json:"name"           example:"Wakeup Crew"`
	AvatarURL     string `json:"avatar_url"     example:"https://wakeup.app/avatars/group.png"`
	LastMessageAt string `json:"last_message_at" example:"2026-05-02T10:42:55.412Z"`
}

// SearchMessageRow is the slim message-search hit. Only the fields the
// search modal needs to render — body excerpt, conversation reference,
// and timestamp. Drill-down via the conversation thread is the path
// for full message context.
type SearchMessageRow struct {
	ID             string `json:"id"              example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ConversationID string `json:"conversation_id" example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	SenderID       string `json:"sender_id"       example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Body           string `json:"body"            example:"hey what time are we meeting?"`
	CreatedAt      string `json:"created_at"      example:"2026-05-02T10:42:55.412Z"`
}

// Search runs the unified search across friends, conversations, and
// messages.
//
// @Summary      Unified search
// @Description  Searches across friends (username/display_name trigram), group conversations (name substring), and messages (body full-text). The optional `types` query param caps the search to a comma-joined subset (`users`, `conversations`, `messages`); empty = all. Each section is hard-capped at 10 results so the mobile global-search modal renders fast — drill-downs use the per-section endpoints. Min query length: 2.
// @Tags         search
// @Produce      json
// @Security     CookieAuth
// @Param        q       query    string  true   "Search query (min 2 chars)"  example("wake")
// @Param        types   query    string  false  "Comma-joined sections to include"  example("users,conversations,messages")
// @Success      200     {object} SearchResponse  "Up to 10 hits per section"
// @Header       200     {string} X-Request-ID    "Echoed request id"
// @Failure      401     {object} ErrorResponse   "Not authenticated"
// @Failure      422     {object} ErrorResponse   "Validation failed (q too short, unknown type)"
// @Failure      429     {object} ErrorResponse   "Rate limited"
// @Failure      500     {object} ErrorResponse   "Internal error"
// @Router       /v1/search [get]
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	types, err := searchsvc.ParseTypes(r.URL.Query().Get("types"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.search.Search(r.Context(), searchsvc.Params{
		UserID: uid, Query: q, Types: types,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}

	// Compute the EFFECTIVE requested set so the response can carry an
	// empty array for "requested but no hits." When the caller passes
	// no types param, the service treats it as "all sections" — mirror
	// that here so we don't drop empty arrays for unspecified types.
	requested := requestedTypes(types)

	out := SearchResponse{}
	if requested[searchsvc.TypeUsers] {
		users := make([]UserResponse, 0, len(res.Users))
		for _, u := range res.Users {
			users = append(users, toUserResponse(u))
		}
		out.Users = &users
	}
	if requested[searchsvc.TypeConversations] {
		convs := make([]SearchConversationRow, 0, len(res.Conversations))
		for _, c := range res.Conversations {
			row := SearchConversationRow{
				ID:            c.ID.String(),
				Type:          string(c.Type),
				LastMessageAt: c.LastMessageAt.Format("2006-01-02T15:04:05.000Z"),
			}
			if c.Name != nil {
				row.Name = *c.Name
			}
			if c.AvatarURL != nil {
				row.AvatarURL = *c.AvatarURL
			}
			convs = append(convs, row)
		}
		out.Conversations = &convs
	}
	if requested[searchsvc.TypeMessages] {
		msgs := make([]SearchMessageRow, 0, len(res.Messages))
		for _, m := range res.Messages {
			msgs = append(msgs, SearchMessageRow{
				ID:             m.ID.String(),
				ConversationID: m.ConversationID.String(),
				SenderID:       m.SenderID.String(),
				Body:           m.Body,
				CreatedAt:      m.CreatedAt.Format("2006-01-02T15:04:05.000Z"),
			})
		}
		out.Messages = &msgs
	}
	WriteJSON(w, http.StatusOK, out)
}

// requestedTypes turns the parsed types slice into a set. nil/empty
// input means "all sections" — same convention as searchsvc.Search.
func requestedTypes(types []searchsvc.Type) map[searchsvc.Type]bool {
	if len(types) == 0 {
		return map[searchsvc.Type]bool{
			searchsvc.TypeUsers:         true,
			searchsvc.TypeConversations: true,
			searchsvc.TypeMessages:      true,
		}
	}
	m := make(map[searchsvc.Type]bool, len(types))
	for _, t := range types {
		m[t] = true
	}
	return m
}
