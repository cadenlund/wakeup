package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// errorWriter mirrors httpapi.WriteError so the ws package doesn't have
// to depend on the httpapi package (which would create an import cycle
// — httpapi depends on ws via the upgrade route registration in
// router.go). The /v1/ws upgrade handler calls this on auth failure.
type errorWriter func(w http.ResponseWriter, r *http.Request, err error)

// Handler hosts the /v1/ws upgrade. Composes the Hub + Bridge + auth
// + conversation service so a connecting client lands fully wired:
//
//  1. Auth: session cookie → user_id (via auth.CurrentUser).
//  2. Lookup: every conversation the user is a member of.
//  3. Subscribe: bridge.Subscribe for `user:<id>:events` and
//     `conv:<id>:messages` for each conv.
//  4. Upgrade: websocket.Accept.
//  5. Register: hub.Register(conn).
//  6. Run: read pump + write pump until disconnect.
//  7. Cleanup: bridge.UnsubscribeAll on exit.
//
// Inbound event routing happens in onMessage:
//   - heartbeat   → no-op (kept-alive timer; absence detection is the
//     ping/pong layer's job).
//   - typing.*    → re-publish on the conv channel with the
//     server-stamped user_id so other instances'
//     bridges fan it out to other members.
//   - presence.set → logged for now; Phase 9 wires the presence
//     service.
type Handler struct {
	hub            *Hub
	bridge         *Bridge
	broker         pubsub.Broker
	auth           *auth.Service
	convs          *convsvc.Service
	unread         UnreadCounter
	logger         *slog.Logger
	allowedOrigins []string
	writeError     errorWriter
}

// UnreadCounter is the slice of message-repo this handler needs to
// embed `unread_total` in heartbeat acks (WAKEUPEXPO.md §7.5). Defining
// it locally keeps the dep small + stub-friendly. Production wiring
// passes *messagerepo.Queries.
type UnreadCounter interface {
	CountUnreadForUser(ctx context.Context, userID uuid.UUID) (int64, error)
}

// HandlerConfig builds the upgrade handler.
type HandlerConfig struct {
	Hub            *Hub
	Bridge         *Bridge
	Broker         pubsub.Broker
	Auth           *auth.Service
	Convs          *convsvc.Service
	Logger         *slog.Logger
	AllowedOrigins []string
	// Unread is optional. When nil, heartbeat acks are sent without
	// the unread_total field — clients that want the count fall back
	// to the X-Unread-Total header on /v1/auth/me.
	Unread UnreadCounter
	// WriteError is the §4.4 envelope writer. Pass httpapi.WriteError
	// from the router. Nil makes auth failures write a bare 401.
	WriteError errorWriter
}

// NewHandler constructs the handler.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Hub == nil {
		return nil, errors.New("ws: HandlerConfig.Hub is required")
	}
	if cfg.Bridge == nil {
		return nil, errors.New("ws: HandlerConfig.Bridge is required")
	}
	if cfg.Broker == nil {
		return nil, errors.New("ws: HandlerConfig.Broker is required")
	}
	if cfg.Auth == nil {
		return nil, errors.New("ws: HandlerConfig.Auth is required")
	}
	if cfg.Convs == nil {
		return nil, errors.New("ws: HandlerConfig.Convs is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	we := cfg.WriteError
	if we == nil {
		we = bareErrorWriter
	}
	return &Handler{
		hub: cfg.Hub, bridge: cfg.Bridge, broker: cfg.Broker,
		auth: cfg.Auth, convs: cfg.Convs, unread: cfg.Unread,
		logger:         logger,
		allowedOrigins: cfg.AllowedOrigins, writeError: we,
	}, nil
}

// Mount attaches /v1/ws onto r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/ws", h.Upgrade)
}

// Upgrade is the /v1/ws handler. Auth is enforced before the upgrade
// — an unauthenticated request gets 401 and never opens a socket.
//
// @Summary      Open the realtime WebSocket connection
// @Description  Upgrades the request to a WebSocket. Auth is enforced before the upgrade via the same session cookie used for REST. The server subscribes the connection to `user:<id>:events` plus `conv:<id>:messages` for every conversation the caller is a member of, and routes inbound `heartbeat` / `typing.*` / `presence.set` events per §7.3. See §7.1 for the JSON envelope format.
// @Tags         realtime
// @Produce      json
// @Security     CookieAuth
// @Success      101  "Switching Protocols (WebSocket established)"
// @Failure      401  "Not authenticated"
// @Failure      429  "Rate limited"
// @Failure      500  "Internal error"
// @Router       /v1/ws [get]
func (h *Handler) Upgrade(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	channels, err := h.channelsForUser(r.Context(), uid)
	if err != nil {
		h.writeError(w, r, apierror.Internal("ws: list user channels").WithCause(err))
		return
	}

	if err := h.bridge.Subscribe(r.Context(), uid, channels...); err != nil {
		h.writeError(w, r, apierror.Internal("ws: subscribe").WithCause(err))
		return
	}

	c, err := websocket.Accept(w, r, AcceptOptions(h.allowedOrigins))
	if err != nil {
		// Accept already wrote a response on its own failure paths; we
		// just need to roll back the subscription so the bus isn't
		// holding state for a conn that never came up.
		h.bridge.UnsubscribeAll(context.Background(), uid)
		return
	}

	conn, err := NewConn(ConnConfig{
		UserID: uid, WS: c, Hub: h.hub, Logger: h.logger,
		OnMessage: h.makeOnMessage(uid),
	})
	if err != nil {
		h.bridge.UnsubscribeAll(context.Background(), uid)
		_ = c.Close(websocket.StatusInternalError, "newconn")
		return
	}
	h.hub.Register(conn)
	defer h.bridge.UnsubscribeAll(context.Background(), uid)

	_ = conn.Run(r.Context())
}

// channelsForUser builds the pubsub channel list this connection
// should subscribe to: one direct user channel + one per conversation
// the caller is a member of. Pages through convs.List exhaustively;
// the caller is bounded by the user's group memberships (each group
// caps at 25 by §4.6, but the user can be in many groups + many
// directs so we paginate to be safe).
func (h *Handler) channelsForUser(ctx context.Context, uid uuid.UUID) ([]string, error) {
	channels := []string{userChannel(uid)}
	var cursor *pagination.Cursor
	for {
		page, err := h.convs.List(ctx, convsvc.ListParams{
			UserID: uid, Cursor: cursor, Limit: pagination.MaxLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("ws: list user conversations: %w", err)
		}
		for _, c := range page.Conversations {
			channels = append(channels, ConvChannel(c.ID))
		}
		if !page.HasMore || page.NextCursor == nil {
			break
		}
		next, err := pagination.Decode(*page.NextCursor)
		if err != nil {
			return nil, fmt.Errorf("ws: decode next cursor: %w", err)
		}
		cursor = next
	}
	return channels, nil
}

// makeOnMessage returns the per-connection inbound-event router. The
// closure binds uid so re-published payloads carry the server-stamped
// user_id (the client never gets to claim a different identity).
func (h *Handler) makeOnMessage(uid uuid.UUID) func(ctx context.Context, raw []byte) error {
	return func(ctx context.Context, raw []byte) error {
		env, err := wsproto.Decode(raw)
		if err != nil {
			return fmt.Errorf("ws: decode inbound: %w", err)
		}
		switch env.Type {
		case wsproto.EventHeartbeat:
			h.replyHeartbeat(ctx, uid)
			return nil
		case wsproto.EventTypingStart, wsproto.EventTypingStop:
			return h.handleTyping(ctx, uid, env)
		case wsproto.EventPresenceSet:
			// Phase 9 wires the presence service. Logging at debug
			// keeps the no-op silent in normal operation.
			h.logger.Debug("ws: presence.set received (Phase 9 not yet implemented)",
				slog.String("user_id", uid.String()),
			)
			return nil
		default:
			return fmt.Errorf("ws: unsupported inbound event type %q", env.Type)
		}
	}
}

// handleTyping re-publishes a typing event on the conversation
// channel with the server-stamped user_id, so other replicas' bridges
// fan it out to the other members.
//
// Membership gate: without this check a malicious client could blast
// typing.start across every conversation in the database — they'd
// know the conv_id from any prior interaction or just guessing.
// convs.Get returns RESOURCE_NOT_FOUND for non-members, which we
// surface to the caller's onMessage as an error (the read pump logs
// it but keeps the conn open). CodeRabbit PR #49.
// replyHeartbeat sends a heartbeat ack back to the client carrying the
// caller's current unread message total — mobile uses it to keep the
// app icon badge accurate without a REST round-trip per heartbeat
// (WAKEUPEXPO.md §7.5). Best-effort: a count or encode failure logs
// but doesn't surface to the caller (the heartbeat itself succeeded).
func (h *Handler) replyHeartbeat(ctx context.Context, uid uuid.UUID) {
	payload := wsproto.HeartbeatPayload{}
	if h.unread != nil {
		n, err := h.unread.CountUnreadForUser(ctx, uid)
		if err != nil {
			h.logger.Warn("ws: heartbeat unread count failed",
				slog.String("user_id", uid.String()),
				slog.String("error", err.Error()),
			)
		} else {
			payload.UnreadTotal = n
		}
	}
	encoded, err := wsproto.Encode(wsproto.EventHeartbeat, payload)
	if err != nil {
		h.logger.Warn("ws: heartbeat encode failed",
			slog.String("user_id", uid.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	h.hub.BroadcastToUser(uid, encoded)
}

func (h *Handler) handleTyping(ctx context.Context, uid uuid.UUID, env wsproto.Envelope) error {
	var p wsproto.TypingPayload
	if err := wsproto.UnmarshalData(env, &p); err != nil {
		return fmt.Errorf("ws: typing payload: %w", err)
	}
	if p.ConversationID == uuid.Nil {
		return errors.New("ws: typing payload missing conversation_id")
	}
	if _, err := h.convs.Get(ctx, uid, p.ConversationID); err != nil {
		return fmt.Errorf("ws: typing membership check: %w", err)
	}
	stamped := wsproto.TypingPayload{ConversationID: p.ConversationID, UserID: &uid}
	encoded, err := wsproto.Encode(env.Type, stamped)
	if err != nil {
		return fmt.Errorf("ws: re-encode typing: %w", err)
	}
	if err := h.broker.Publish(ctx, ConvChannel(p.ConversationID), encoded); err != nil {
		return fmt.Errorf("ws: publish typing: %w", err)
	}
	return nil
}

// ConvChannel returns the §4.5 pubsub channel name for a conversation.
func ConvChannel(id uuid.UUID) string {
	return fmt.Sprintf("conv:%s:messages", id)
}

// userChannel returns the §4.5 pubsub channel name for a single user.
func userChannel(id uuid.UUID) string {
	return fmt.Sprintf("user:%s:events", id)
}

// bareErrorWriter is the fallback when the caller didn't pass a
// router-side error writer. Writes a plain 401/500 — the production
// router should always pass httpapi.WriteError so the §4.4 envelope
// is preserved.
func bareErrorWriter(w http.ResponseWriter, _ *http.Request, err error) {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		http.Error(w, apiErr.Message, apiErr.HTTPStatus())
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
