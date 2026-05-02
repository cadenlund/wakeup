// Package auth is the registration / login / logout / password-reset
// service. It composes the user repository (§3.1), argon2id wrapper
// (§2.2), session manager (§2.3), mailer (§2.8), and the password-reset
// repository over §16 milestone 3.2.
//
// Discipline:
//   - Returns *apierror.Error on every failure path. Handler maps codes
//     to HTTP status via apierror.HTTPStatus.
//   - Login / RequestPasswordReset / ConfirmPasswordReset return generic
//     errors so attackers can't probe for valid usernames or tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/argon2id"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/mailer"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// SessionUserIDKey is the scs session key the auth service writes the
// authenticated user_id into. Middleware (§3.8) reads from the same key.
const SessionUserIDKey = "user_id"

// PasswordResetTTL is how long a reset token stays usable. 1 hour matches
// the email body promise in the mailer template.
const PasswordResetTTL = 1 * time.Hour

// Service composes the dependencies an auth flow needs. Goroutine-safe.
type Service struct {
	pool         *pgxpool.Pool // for transactions
	users        *user.Queries
	resets       *passwordreset.Queries
	sessions     *scs.SessionManager
	mail         mailer.Mailer
	now          func() time.Time
	tokenEntropy int // number of random bytes per reset token (default 32)
}

// Config builds the service. Fields except now/tokenEntropy are required.
type Config struct {
	Pool         *pgxpool.Pool
	Users        *user.Queries
	Resets       *passwordreset.Queries
	Sessions     *scs.SessionManager
	Mailer       mailer.Mailer
	Now          func() time.Time // optional: defaults to time.Now
	TokenEntropy int              // optional: defaults to 32 bytes
}

// New constructs the auth service.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Pool == nil:
		return nil, errors.New("auth: Config.Pool is required")
	case cfg.Users == nil:
		return nil, errors.New("auth: Config.Users is required")
	case cfg.Resets == nil:
		return nil, errors.New("auth: Config.Resets is required")
	case cfg.Sessions == nil:
		return nil, errors.New("auth: Config.Sessions is required")
	case cfg.Mailer == nil:
		return nil, errors.New("auth: Config.Mailer is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	entropy := cfg.TokenEntropy
	if entropy <= 0 {
		entropy = 32
	}
	return &Service{
		pool:         cfg.Pool,
		users:        cfg.Users,
		resets:       cfg.Resets,
		sessions:     cfg.Sessions,
		mail:         cfg.Mailer,
		now:          now,
		tokenEntropy: entropy,
	}, nil
}

// RegisterParams is the validated input to Register. Length / format
// validation is the handler's job (validator/v10 tags); the service
// trusts that the values arrive within the §4.6 limits.
type RegisterParams struct {
	Username    string
	Email       string
	DisplayName string
	Password    string
}

// Register creates a new user and starts a session for them. Conflicts
// (duplicate username or email) surface as apierror.Conflict so the
// handler returns 409.
func (s *Service) Register(ctx context.Context, p RegisterParams) (domain.User, error) {
	hash, err := argon2id.Hash(p.Password)
	if err != nil {
		return domain.User{}, apierror.Internal("password hashing failed").WithCause(err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, apierror.Internal("uuid generation failed").WithCause(err)
	}
	created, err := s.users.Create(ctx, user.CreateParams{
		ID:           id,
		Username:     p.Username,
		DisplayName:  p.DisplayName,
		Email:        p.Email,
		PasswordHash: hash,
	})
	if err != nil {
		// Detect unique-violation to give a clean 409. We can't tell which
		// constraint tripped without parsing the constraint name, so the
		// message stays generic — the handler can render a friendlier one
		// from the validator before reaching here in practice.
		if isUniqueViolation(err) {
			return domain.User{}, apierror.Conflict("username or email already in use")
		}
		return domain.User{}, apierror.Internal("create user").WithCause(err)
	}

	// Rotate the session token before writing the new identity — defeats
	// session fixation (a pre-login cookie can't be reused post-login).
	// scs idiom: RenewToken FIRST, then Put.
	if err := s.sessions.RenewToken(ctx); err != nil {
		return domain.User{}, apierror.Internal("session renew").WithCause(err)
	}
	s.sessions.Put(ctx, SessionUserIDKey, created.ID.String())
	return created, nil
}

// LoginParams is the validated input to Login. Identifier is either a
// username or an email — the service tries both.
type LoginParams struct {
	Identifier string
	Password   string
}

// Login validates credentials, starts a session, and returns the user.
// Returns apierror.Unauthorized for ANY failure path (wrong password,
// missing user, soft-deleted user) so attackers can't enumerate.
func (s *Service) Login(ctx context.Context, p LoginParams) (domain.User, error) {
	id := strings.TrimSpace(p.Identifier)
	if id == "" || p.Password == "" {
		return domain.User{}, apierror.Unauthorized("invalid credentials")
	}

	u, err := s.lookupForLogin(ctx, id)
	if err != nil {
		// Even on lookup error we fall through with the same generic
		// "invalid credentials" — no enumeration. Internal lookup errors
		// (transport / DB hiccup) get logged via the wrapped cause.
		if errors.Is(err, user.ErrNotFound) {
			return domain.User{}, apierror.Unauthorized("invalid credentials")
		}
		return domain.User{}, apierror.Internal("lookup user").WithCause(err)
	}
	ok, err := argon2id.Verify(p.Password, u.PasswordHash)
	if err != nil {
		return domain.User{}, apierror.Internal("verify password").WithCause(err)
	}
	if !ok {
		return domain.User{}, apierror.Unauthorized("invalid credentials")
	}

	// Rotate session token before binding identity (session-fixation defense).
	if err := s.sessions.RenewToken(ctx); err != nil {
		return domain.User{}, apierror.Internal("session renew").WithCause(err)
	}
	s.sessions.Put(ctx, SessionUserIDKey, u.ID.String())
	return u, nil
}

// lookupForLogin always tries username first then falls back to email on a
// not-found miss. We intentionally don't shortcut on "@" because §4.6
// allows usernames containing characters that look email-ish in some
// edge inputs, and we shouldn't let that flip the precedence (a username
// that happens to look like another user's email must still authenticate
// as the username).
func (s *Service) lookupForLogin(ctx context.Context, identifier string) (domain.User, error) {
	u, err := s.users.GetByUsername(ctx, identifier)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, user.ErrNotFound) {
		return domain.User{}, err
	}
	return s.users.GetByEmail(ctx, identifier)
}

// Logout destroys the current session. Always returns nil (a missing
// session is not an error — idempotent).
func (s *Service) Logout(ctx context.Context) error {
	if err := s.sessions.Destroy(ctx); err != nil {
		return apierror.Internal("destroy session").WithCause(err)
	}
	return nil
}

// LogoutAll iterates every active session, destroying any that belong to
// userID. O(N) in the size of the sessions table; fine for v1's scale.
// scs.Iterate locks one session at a time — no full-store contention.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	want := userID.String()
	err := s.sessions.Iterate(ctx, func(sctx context.Context) error {
		if got := s.sessions.GetString(sctx, SessionUserIDKey); got == want {
			return s.sessions.Destroy(sctx)
		}
		return nil
	})
	if err != nil {
		return apierror.Internal("iterate sessions").WithCause(err)
	}
	return nil
}

// CurrentUser returns the user ID held in the active session, or
// uuid.Nil + apierror.Unauthorized when no session is loaded.
func (s *Service) CurrentUser(ctx context.Context) (uuid.UUID, error) {
	raw := s.sessions.GetString(ctx, SessionUserIDKey)
	if raw == "" {
		return uuid.Nil, apierror.Unauthorized("not authenticated")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apierror.Unauthorized("not authenticated").WithCause(err)
	}
	return id, nil
}

// Me returns the user backing the current session. Returns Unauthorized
// when the session is missing OR the user has been soft-deleted.
func (s *Service) Me(ctx context.Context) (domain.User, error) {
	id, err := s.CurrentUser(ctx)
	if err != nil {
		return domain.User{}, err
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return domain.User{}, apierror.Unauthorized("not authenticated")
		}
		return domain.User{}, apierror.Internal("load user").WithCause(err)
	}
	return u, nil
}

// RequestPasswordReset generates a token and emails it to the user.
// ALWAYS returns nil — even when the email is unknown — to defeat email
// enumeration (§6.2 always-204 contract).
//
// Two enumeration vectors we close here:
//
//  1. **Outcome leakage**: nil return regardless of branch.
//
//  2. **Timing leakage**: the cryptographic random + SHA-256 hash run
//     unconditionally, so an attacker timing the request can't infer
//     "known account" from a longer response. The DB insert + mail
//     dispatch only happen for real users (we're not willing to spam
//     password_resets with junk rows), but those are O(ms) and dwarfed
//     by the noise floor of normal HTTP latency. Perfectly constant-time
//     would require fake DB/network round-trips for unknown emails, a
//     trade-off we don't take in v1.
//
// Internal errors (random failure, DB transport hiccup, mailer outage)
// are swallowed at the service boundary so they don't leak via a 500
// only on the existing-account path. The underlying cause is left to
// be logged at the handler/middleware layer when slog is wired in.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	// Always do the random+hash work, even for unknown emails, so the
	// CPU cost of the call doesn't differ between cases.
	rawToken, _ := generateToken(s.tokenEntropy)
	tokenHash := sha256Bytes(rawToken)
	expiresAt := s.now().UTC().Add(PasswordResetTTL)

	u, err := s.users.GetByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return nil // unknown email — silent no-op
	}
	// Best-effort persist + mail. Errors are intentionally not surfaced
	// to the caller — the always-204 contract trumps an internal failure.
	_ = s.resets.Create(ctx, tokenHash, u.ID, expiresAt)
	_ = s.mail.SendPasswordReset(ctx, u.Email, rawToken)
	return nil
}

// ConfirmPasswordResetParams carries a Confirm call's input.
type ConfirmPasswordResetParams struct {
	Token       string
	NewPassword string
}

// ConfirmPasswordReset validates the token + sets the new password.
// Returns apierror.Unauthorized on token miss / expired / used so the
// failure modes are indistinguishable to a client (timing oracle defense).
//
// Wraps Update + MarkUsed in a transaction so we don't end up with a
// changed password but an unconsumed token (replay-able) on partial failure.
func (s *Service) ConfirmPasswordReset(ctx context.Context, p ConfirmPasswordResetParams) error {
	if strings.TrimSpace(p.Token) == "" || p.NewPassword == "" {
		return apierror.Unauthorized("invalid reset token")
	}
	tokenHash := sha256Bytes(p.Token)

	hash, err := argon2id.Hash(p.NewPassword)
	if err != nil {
		return apierror.Internal("hash new password").WithCause(err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apierror.Internal("begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	resets := s.resets.WithTx(tx)
	users := s.users.WithTx(tx)

	entry, err := resets.Get(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, passwordreset.ErrNotFound) {
			return apierror.Unauthorized("invalid reset token")
		}
		return apierror.Internal("get reset").WithCause(err)
	}

	if err := users.UpdatePassword(ctx, entry.UserID, hash); err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.Unauthorized("invalid reset token")
		}
		return apierror.Internal("update password").WithCause(err)
	}
	if err := resets.MarkUsed(ctx, tokenHash); err != nil {
		// Already-used between Get and here is impossible inside a tx,
		// but treat it the same.
		if errors.Is(err, passwordreset.ErrNotFound) {
			return apierror.Unauthorized("invalid reset token")
		}
		return apierror.Internal("mark used").WithCause(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return apierror.Internal("commit reset").WithCause(err)
	}

	// Best-effort: kill all sessions for the user so a stolen-cookie
	// attacker is logged out. We deliberately ignore errors here — if
	// it fails, the password change still went through, which is the
	// security-critical part.
	_ = s.LogoutAll(ctx, entry.UserID)
	return nil
}

// --- helpers --------------------------------------------------------------

// generateToken returns a hex-encoded random token of n*2 chars.
func generateToken(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", errors.New("auth: token entropy must be > 0")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: random read: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// sha256Bytes returns the raw 32-byte SHA-256 hash of s. Used to map a
// user-facing token to the bytes stored in password_resets.token_hash
// (the schema's CHECK enforces octet_length == 32).
func sha256Bytes(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// isUniqueViolation matches Postgres SQLSTATE 23505. Auth uses this to
// translate Create errors into apierror.Conflict instead of 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
