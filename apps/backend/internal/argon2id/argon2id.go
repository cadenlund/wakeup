// Package argon2id wraps github.com/alexedwards/argon2id with the
// project-locked parameters from WAKEUP.md §8.1. The whole codebase hashes
// and verifies passwords through this package — never the underlying library
// directly — so a future tuning change is one edit instead of N call sites.
package argon2id

import (
	"errors"
	"fmt"

	axe "github.com/alexedwards/argon2id"
)

// ErrEmptyPassword is returned when a caller asks to Hash or Verify with an
// empty password. Empty passwords are valid argon2id input but always
// represent a programmer bug here — the validator layer rejects empty
// passwords at the request boundary, so anything reaching this package with
// "" is a sign of mis-wired plumbing.
var ErrEmptyPassword = errors.New("argon2id: password is empty")

// Params is the locked argon2id parameter set used by every Hash call.
// Tuned for ~50ms per hash on commodity hardware (sufficient for an
// online-login workload). Re-tuning requires deciding how to handle
// previously-hashed passwords — verification still works against any
// argon2id parameter set, so old hashes remain valid.
var Params = &axe.Params{
	Memory:      64 * 1024, // 64 MiB
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

// Hash returns an encoded argon2id hash for password using the locked Params.
// Refuses empty input with ErrEmptyPassword.
func Hash(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	h, err := axe.CreateHash(password, Params)
	if err != nil {
		return "", fmt.Errorf("argon2id: hash: %w", err)
	}
	return h, nil
}

// Verify reports whether password matches the encoded argon2id hash.
// Returns:
//   - (true, nil)  on a match
//   - (false, nil) on a mismatch (the hash decoded fine, the password was wrong)
//   - (false, err) when the hash is malformed or the password is empty
//
// Callers that just need a yes/no can check err != nil first; an err means
// "this isn't a usable hash," which is distinct from "this hash didn't match."
func Verify(password, hash string) (bool, error) {
	if password == "" {
		return false, ErrEmptyPassword
	}
	if hash == "" {
		return false, fmt.Errorf("argon2id: hash is empty")
	}
	ok, err := axe.ComparePasswordAndHash(password, hash)
	if err != nil {
		return false, fmt.Errorf("argon2id: verify: %w", err)
	}
	return ok, nil
}
