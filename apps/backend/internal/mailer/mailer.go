// Package mailer wraps Resend (github.com/resend/resend-go/v2) with the
// project-locked surface from §10.1: one method, SendPasswordReset, used
// only by the password-reset flow. Templates live inline as Go strings —
// no template files for v1.
//
// In tests we point a custom Resend client at an httptest.Server (see
// mailer_test.go) so CI never makes a live API call.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/resend/resend-go/v2"
)

// Mailer is the §10.1 mailer interface, satisfied by *Resend. The interface
// definition lives here so handler/service code can take the small surface
// it actually uses without depending on the SDK.
type Mailer interface {
	SendPasswordReset(ctx context.Context, to, token string) error
}

// Resend is the production implementation. Goroutine-safe; share one
// instance for the whole process.
type Resend struct {
	client       *resend.Client
	from         string // e.g. "Wakeup <no-reply@wakeup.app>"
	resetURLBase string // e.g. "https://wakeup.app/auth/reset?token="
}

// Config carries the runtime values pulled from .env via internal/config.
type Config struct {
	APIKey       string // RESEND_API_KEY
	FromEmail    string // RESEND_FROM_EMAIL — recipient sees this as the From line
	ResetURLBase string // base of the link emailed to the user; the token is appended verbatim
}

// New constructs a Resend mailer using the SDK's default HTTP client.
// Returns an error when any required Config field is blank — better to fail
// at startup than to silently accept the bad config and surface 500s later.
func New(cfg Config) (*Resend, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return NewWithClient(resend.NewClient(cfg.APIKey), cfg)
}

// NewWithClient is the test-injection escape hatch. Tests build a
// *resend.Client with a custom HTTP transport pointed at httptest.Server
// and pass it here.
func NewWithClient(client *resend.Client, cfg Config) (*Resend, error) {
	if client == nil {
		return nil, errors.New("mailer: NewWithClient: client is nil")
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return &Resend{
		client:       client,
		from:         cfg.FromEmail,
		resetURLBase: cfg.ResetURLBase,
	}, nil
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("mailer: Config.APIKey is required")
	}
	if strings.TrimSpace(cfg.FromEmail) == "" {
		return errors.New("mailer: Config.FromEmail is required")
	}
	if strings.TrimSpace(cfg.ResetURLBase) == "" {
		return errors.New("mailer: Config.ResetURLBase is required")
	}
	// ParseRequestURI rejects relative URLs (url.Parse accepts them).
	// We then explicitly require Scheme + Host so a path-only string like
	// "/auth/reset?token=" doesn't slip through.
	u, err := url.ParseRequestURI(cfg.ResetURLBase)
	if err != nil {
		return fmt.Errorf("mailer: Config.ResetURLBase is not a valid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("mailer: Config.ResetURLBase must be an absolute URL with scheme + host")
	}
	return nil
}

// SendPasswordReset sends a password-reset email with a link the user
// clicks to land on the reset-confirm page. The token is appended verbatim
// to ResetURLBase — the URL builder is intentionally simple because the
// auth service generates URL-safe tokens (per §8.1 password-reset table).
func (r *Resend) SendPasswordReset(ctx context.Context, to, token string) error {
	if strings.TrimSpace(to) == "" {
		return errors.New("mailer: SendPasswordReset: to is empty")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("mailer: SendPasswordReset: token is empty")
	}

	link := r.resetURLBase + url.QueryEscape(token)
	html := passwordResetHTML(link)
	plain := passwordResetText(link)

	params := &resend.SendEmailRequest{
		From:    r.from,
		To:      []string{to},
		Subject: "Reset your Wakeup password",
		Html:    html,
		Text:    plain,
	}
	if _, err := r.client.Emails.SendWithContext(ctx, params); err != nil {
		return fmt.Errorf("mailer: SendPasswordReset: %w", err)
	}
	return nil
}

// passwordResetHTML / passwordResetText are inline templates per §10.1.
// Kept tiny and brand-free — anything richer is post-v1.
func passwordResetHTML(link string) string {
	return `<!doctype html>
<html><body style="font-family: system-ui, sans-serif; max-width: 480px; margin: 24px auto;">
<p>You asked to reset your Wakeup password. Click the link below to choose a new one. The link expires in 1 hour.</p>
<p><a href="` + link + `" style="background:#111;color:#fff;text-decoration:none;padding:10px 16px;border-radius:6px;display:inline-block;">Reset password</a></p>
<p>If you didn't ask for this, ignore this email.</p>
</body></html>`
}

func passwordResetText(link string) string {
	return "You asked to reset your Wakeup password. Open this link to choose a new one (expires in 1 hour):\n\n" +
		link +
		"\n\nIf you didn't ask for this, ignore this email."
}

// Compile-time guard that *Resend satisfies the Mailer interface.
var _ Mailer = (*Resend)(nil)
