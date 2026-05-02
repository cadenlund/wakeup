// Package pushnotif wraps the Expo Push HTTP API (https://exp.host/--/api/v2/push/send)
// for §10.2 — offline push delivery. No Expo SDK; just a JSON POST.
//
// Authenticated via the operator's EXPO_ACCESS_TOKEN (required by Expo's
// enhanced security mode). Unauthenticated requests are rate-limited and
// flagged; we always send the bearer header.
//
// Per §11 / §16 milestone 11.5, callers are notification.Service —
// MessageService.Send / FriendService.SendRequest / CallService.InitiateCall
// invoke this layer for offline recipients gated by NotificationPreferences.
package pushnotif

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is Expo's production push endpoint. Tests override via
// NewWithHTTP to point at httptest.Server.
const DefaultEndpoint = "https://exp.host/--/api/v2/push/send"

// Notification is the §10.2 payload. Title + Body show in the OS toast;
// Data is opaque JSON the mobile client routes on (e.g. {"type":"message","conversation_id":"..."}).
type Notification struct {
	Title string
	Body  string
	Data  map[string]any
}

// Pusher is the §10.2 contract — what notification.Service depends on.
type Pusher interface {
	Send(ctx context.Context, tokens []string, n Notification) error
}

// ExpoPusher is the production Pusher backed by Expo's HTTP API.
// Goroutine-safe; share one for the process.
type ExpoPusher struct {
	client      *http.Client
	endpoint    string
	accessToken string
}

// Config carries .env values. AccessToken is required (Expo enforces enhanced
// security in production); blank tokens fail at construction.
type Config struct {
	AccessToken string // EXPO_ACCESS_TOKEN
}

// New constructs an ExpoPusher with a default *http.Client (10s timeout).
// Returns an error when AccessToken is blank — better to fail at startup
// than to silently 401 every push later.
func New(cfg Config) (*ExpoPusher, error) {
	return NewWithHTTP(cfg, &http.Client{Timeout: 10 * time.Second}, DefaultEndpoint)
}

// NewWithHTTP is the test-injection escape hatch. Tests pass an httptest
// .Server's URL as endpoint and an http.Client with no extra config.
func NewWithHTTP(cfg Config, client *http.Client, endpoint string) (*ExpoPusher, error) {
	// Trim AccessToken before storing — leading/trailing whitespace would
	// silently corrupt the Authorization header otherwise.
	token := strings.TrimSpace(cfg.AccessToken)
	if token == "" {
		return nil, errors.New("pushnotif: Config.AccessToken is required")
	}
	if client == nil {
		return nil, errors.New("pushnotif: NewWithHTTP: client is nil")
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("pushnotif: NewWithHTTP: endpoint is empty")
	}
	return &ExpoPusher{client: client, endpoint: endpoint, accessToken: token}, nil
}

// expoMessage is one Expo Push message. Per Expo docs, the `to` field can
// be either a single token string or an array of tokens — we use the array
// form so a single POST broadcasts to every token in the slice.
type expoMessage struct {
	To    []string       `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Data  map[string]any `json:"data,omitempty"`
}

// expoResponse is the partial response shape we care about. Each `data[i]`
// maps 1:1 to the input tokens; status="ok" with id, or status="error" with
// message + details. We don't surface per-token error breakdown to callers
// in v1; we report a single error if ANY recipient failed, including the
// per-recipient details so the operator can see them in slog.
type expoResponse struct {
	Data   []expoTicket   `json:"data"`
	Errors []expoTopError `json:"errors,omitempty"`
}

type expoTicket struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	ID      string         `json:"id,omitempty"`
}

type expoTopError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Send broadcasts n to every token in tokens via one POST. Returns nil on
// HTTP 200 with all tickets status="ok". An empty tokens slice is a no-op
// (no error) — callers commonly compute the slice from "users without an
// active WS conn" and a zero count just means everyone was online.
func (p *ExpoPusher) Send(ctx context.Context, tokens []string, n Notification) error {
	if len(tokens) == 0 {
		return nil
	}
	if strings.TrimSpace(n.Title) == "" || strings.TrimSpace(n.Body) == "" {
		return errors.New("pushnotif: Send: Title and Body are required")
	}
	// Defensive copy of Data so caller-side mutations after Send returns
	// can't corrupt the JSON we're about to encode.
	dataCopy := make(map[string]any, len(n.Data))
	for k, v := range n.Data {
		dataCopy[k] = v
	}
	msg := expoMessage{
		To:    append([]string(nil), tokens...),
		Title: n.Title,
		Body:  n.Body,
		Data:  dataCopy,
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("pushnotif: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pushnotif: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("pushnotif: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("pushnotif: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pushnotif: expo returned %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var parsed expoResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("pushnotif: parse response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return fmt.Errorf("pushnotif: expo top-level error: %s (%s)",
			parsed.Errors[0].Message, parsed.Errors[0].Code)
	}
	// Expo returns one ticket per input token. If counts don't match, the
	// response is malformed or some recipients are silently unresolved —
	// surface that rather than reporting success.
	if len(parsed.Data) != len(tokens) {
		return fmt.Errorf("pushnotif: ticket count mismatch: sent=%d received=%d",
			len(tokens), len(parsed.Data))
	}
	for _, t := range parsed.Data {
		if t.Status != "ok" {
			return fmt.Errorf("pushnotif: ticket error: status=%q message=%q",
				t.Status, t.Message)
		}
	}
	return nil
}

// truncate keeps log lines from including a 4MB Expo response body verbatim.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Compile-time guard.
var _ Pusher = (*ExpoPusher)(nil)
