package argon2id_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/argon2id"
)

func TestHash_VerifyRoundTrip(t *testing.T) {
	t.Parallel()
	const pw = "correct-horse-battery-staple"
	h, err := argon2id.Hash(pw)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	// Argon2id hashes start with `$argon2id$` per the PHC encoding spec —
	// confirm the wrapper actually returned the right algorithm.
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("Hash output should be argon2id-encoded, got prefix: %q", h[:min(20, len(h))])
	}

	ok, err := argon2id.Verify(pw, h)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify returned false for correct password")
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	t.Parallel()
	h, err := argon2id.Hash("right")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := argon2id.Verify("wrong", h)
	if err != nil {
		t.Fatalf("Verify should not error on a wrong password: %v", err)
	}
	if ok {
		t.Fatal("Verify returned true for wrong password")
	}
}

func TestVerify_MalformedHash(t *testing.T) {
	t.Parallel()
	cases := []string{
		"not-a-hash",
		"$argon2id$",                   // prefix only
		"$argon2id$v=19$",              // missing params + salt + hash
		"$argon2id$v=19$m=65536$wrong", // truncated
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			t.Parallel()
			ok, err := argon2id.Verify("anypassword", h)
			if err == nil {
				t.Fatalf("expected malformed-hash error for %q, got ok=%v err=nil", h, ok)
			}
			if ok {
				t.Fatalf("malformed hash should not produce ok=true, got: %q", h)
			}
		})
	}
}

func TestHash_RejectsEmptyPassword(t *testing.T) {
	t.Parallel()
	_, err := argon2id.Hash("")
	if !errors.Is(err, argon2id.ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got: %v", err)
	}
}

func TestVerify_RejectsEmptyPassword(t *testing.T) {
	t.Parallel()
	// Even with a valid-looking hash, verifying against an empty password
	// must fail with ErrEmptyPassword — never silently return false.
	h, err := argon2id.Hash("real")
	if err != nil {
		t.Fatalf("setup Hash: %v", err)
	}
	_, err = argon2id.Verify("", h)
	if !errors.Is(err, argon2id.ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got: %v", err)
	}
}

func TestVerify_RejectsEmptyHash(t *testing.T) {
	t.Parallel()
	_, err := argon2id.Verify("password", "")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
}

// Hash output should be unique for the same input (each call generates a
// fresh salt). This guards against an accidental refactor that turns Hash
// into a deterministic function.
// Params() must return a defensive copy. Mutating what the accessor returns
// must not change the parameters subsequent Hash calls use.
func TestParams_AccessorReturnsCopy(t *testing.T) {
	t.Parallel()
	got := argon2id.Params()
	if got.Memory != 64*1024 || got.Iterations != 3 || got.Parallelism != 2 ||
		got.SaltLength != 16 || got.KeyLength != 32 {
		t.Fatalf("Params() returned unexpected values: %+v", got)
	}
	// Mutating the returned struct must NOT change the package state.
	got.Memory = 1
	got2 := argon2id.Params()
	if got2.Memory != 64*1024 {
		t.Fatalf("Params() leaked the underlying pointer: got2.Memory = %d", got2.Memory)
	}
}

func TestHash_UsesFreshSaltPerCall(t *testing.T) {
	t.Parallel()
	a, err := argon2id.Hash("same")
	if err != nil {
		t.Fatalf("Hash a: %v", err)
	}
	b, err := argon2id.Hash("same")
	if err != nil {
		t.Fatalf("Hash b: %v", err)
	}
	if a == b {
		t.Fatal("two Hash calls returned identical output — salt should differ")
	}
}
