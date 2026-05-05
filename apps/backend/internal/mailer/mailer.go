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
	client          *resend.Client
	from            string // e.g. "Wakeup <onboarding@resend.dev>"
	resetAppURLBase string // e.g. "wakeup://reset?token="
	resetWebURLBase string // e.g. "http://localhost:8081/reset?token="
}

// Config carries the runtime values pulled from .env via internal/config.
//
// The reset email renders two buttons — "Open in app" (deep link, via
// ResetAppURLBase) and "Open in browser" (https URL, via ResetWebURLBase) —
// so the recipient can pick whichever client they have. Both are required;
// see internal/config and cmd/server/main.go for the env→Config wiring and
// the local defaults.
type Config struct {
	APIKey          string // RESEND_API_KEY
	FromEmail       string // RESEND_FROM_EMAIL — recipient sees this as the From line
	ResetAppURLBase string // deep-link base, e.g. "wakeup://reset?token="
	ResetWebURLBase string // browser base, e.g. "https://app.example.com/reset?token="
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
		client:          client,
		from:            cfg.FromEmail,
		resetAppURLBase: cfg.ResetAppURLBase,
		resetWebURLBase: cfg.ResetWebURLBase,
	}, nil
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("mailer: Config.APIKey is required")
	}
	if strings.TrimSpace(cfg.FromEmail) == "" {
		return errors.New("mailer: Config.FromEmail is required")
	}
	if err := validateResetURL("ResetAppURLBase", cfg.ResetAppURLBase); err != nil {
		return err
	}
	if err := validateResetURL("ResetWebURLBase", cfg.ResetWebURLBase); err != nil {
		return err
	}
	return nil
}

// validateResetURL enforces the same shape on both reset bases: non-blank,
// parseable as an absolute URI, and with a scheme+host. Custom schemes
// like `wakeup://reset?token=` parse with host="reset" so the same check
// works for app deep links and web URLs.
func validateResetURL(field, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("mailer: Config.%s is required", field)
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("mailer: Config.%s is not a valid URL: %w", field, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("mailer: Config.%s must be an absolute URL with scheme + host", field)
	}
	return nil
}

// SendPasswordReset sends a password-reset email with two buttons — the
// app deep link and the browser URL — so the recipient can pick whichever
// client they have. The token is URL-escaped and appended verbatim to each
// base; the auth service generates URL-safe tokens (§8.1) so the simple
// concatenation is fine.
func (r *Resend) SendPasswordReset(ctx context.Context, to, token string) error {
	if strings.TrimSpace(to) == "" {
		return errors.New("mailer: SendPasswordReset: to is empty")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("mailer: SendPasswordReset: token is empty")
	}

	escaped := url.QueryEscape(token)
	appLink := r.resetAppURLBase + escaped
	webLink := r.resetWebURLBase + escaped
	html := passwordResetHTML(appLink, webLink)
	plain := passwordResetText(appLink, webLink)

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
func passwordResetHTML(appLink, webLink string) string {
	const btn = `display:inline-block;padding:10px 16px;border-radius:6px;text-decoration:none;font-weight:600;`
	return `<!doctype html>
<html><body style="font-family: system-ui, sans-serif; max-width: 480px; margin: 24px auto;">
<p>You asked to reset your Wakeup password. Pick whichever opens for you — the link expires in 1 hour.</p>
<p style="margin:20px 0;">
<a href="` + appLink + `" style="` + btn + `background:#111;color:#fff;margin-right:8px;">Open in app</a>
<a href="` + webLink + `" style="` + btn + `background:transparent;color:#111;border:1px solid #111;">Open in browser</a>
</p>
<p>If you didn't ask for this, ignore this email.</p>
</body></html>`
}

func passwordResetText(appLink, webLink string) string {
	return "You asked to reset your Wakeup password. Open one of these to choose a new one (expires in 1 hour):\n\n" +
		"In the app: " + appLink + "\n" +
		"In a browser: " + webLink + "\n\n" +
		"If you didn't ask for this, ignore this email."
}

// Compile-time guard that *Resend satisfies the Mailer interface.
var _ Mailer = (*Resend)(nil)
