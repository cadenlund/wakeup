// Bulk seed-users tool. Inserts a configurable number of synthetic
// users into the local dev database — useful for stress-testing the
// chats list, friends search, and global search at realistic scale.
//
// Usernames are sequential ("user1", "user2", ...) so a single
// search query like "user" surfaces every seeded row at once — the
// whole point of the tool is exercising the global-search modal
// at 1000-row scale. Display names stay randomised so the visual
// list isn't a wall of identical glyphs.
//
// Usage:
//
//	DATABASE_URL=postgres://wakeup:wakeup@localhost:5432/wakeup \
//	  go run ./apps/backend/cmd/seed-users -count 1000
//
// Flags:
//
//	-count    number of users to create (default 1000)
//	-prefix   username prefix; usernames become <prefix>1, <prefix>2,
//	          ... so multiple seed runs can co-exist without collision
//	          (default "user")
//	-start    starting integer for the username suffix (default 1)
//	-password password set on every seeded account (default "Password123!")
//
// Skips users whose username already exists so re-running with the
// same prefix+range is idempotent. Hashes the password ONCE and
// reuses the digest for every row — argon2id is intentionally slow
// per hash (~150ms), so 1000 fresh hashes would take ~2.5 minutes.
// Reusing the digest is fine for a seed tool because the resulting
// rows aren't security-meaningful — they all share the same
// password by design.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/argon2id"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// firstNames + lastNames produce believable display names like
// "Avery Mitchell" / "Noah Patel" so a friends list at 1000-row
// scale isn't visually identical noise. Drawn from the Social
// Security baby-name top-200 + a globally-spread surname list.
var firstNames = []string{
	"Avery", "Noah", "Mia", "Liam", "Olivia", "Ethan", "Sophia", "Mason",
	"Isabella", "Logan", "Ava", "Lucas", "Charlotte", "Aiden", "Amelia",
	"Caden", "Harper", "Jackson", "Evelyn", "Sebastian", "Abigail",
	"Michael", "Emily", "Daniel", "Elizabeth", "Henry", "Sofia", "Owen",
	"Madison", "Jack", "Scarlett", "Wyatt", "Victoria", "Carter", "Aria",
	"Julian", "Grace", "Levi", "Chloe", "Isaac", "Camila", "Luca",
	"Penelope", "Theodore", "Riley", "Anthony", "Layla", "Asher", "Nora",
	"Mateo", "Hannah", "Leo", "Lily", "David", "Aurora", "Joshua",
	"Violet", "Andrew", "Zoey", "Christopher", "Stella", "Joseph",
	"Hazel", "Dylan", "Bella", "Ezra", "Ellie", "Hudson", "Paisley",
	"Charles", "Audrey", "Christian", "Skylar", "Maverick", "Savannah",
}

var lastNames = []string{
	"Mitchell", "Patel", "Garcia", "Nguyen", "Kim", "Lopez", "Reyes",
	"Taylor", "Singh", "Cohen", "Brooks", "Hayes", "Carter", "Bennett",
	"Foster", "Sullivan", "Murphy", "Hughes", "Bell", "Coleman",
	"Jenkins", "Perry", "Powell", "Long", "Patterson", "Hughes",
	"Flores", "Washington", "Butler", "Simmons", "Foster", "Gonzales",
	"Bryant", "Alexander", "Russell", "Griffin", "Diaz", "Hayes", "Myers",
	"Ford", "Hamilton", "Graham", "Sullivan", "Wallace", "Woods", "Cole",
	"West", "Jordan", "Owens", "Reynolds", "Fisher", "Ellis", "Harrison",
	"Gibson", "Mcdonald", "Cruz", "Marshall", "Ortiz", "Gomez", "Murray",
	"Freeman", "Wells", "Webb", "Simpson", "Stevens", "Tucker", "Porter",
	"Hunter", "Hicks", "Crawford", "Henry", "Boyd", "Mason", "Morales",
}

func main() {
	count := flag.Int("count", 1000, "number of users to create")
	prefix := flag.String("prefix", "user", "username prefix; usernames become <prefix>1, <prefix>2, ...")
	start := flag.Int("start", 1, "starting integer for the username suffix")
	password := flag.String("password", "Password123!", "password set on every seeded account")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wakeup:wakeup@localhost:5432/wakeup?sslmode=disable"
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal(logger, "connect", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fatal(logger, "ping", err)
	}

	hash, err := argon2id.Hash(*password)
	if err != nil {
		fatal(logger, "hash password", err)
	}

	repo := userrepo.New(pool)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	created, skipped := 0, 0
	for i := 0; i < *count; i++ {
		id, err := uuid.NewV7()
		if err != nil {
			fatal(logger, "uuid", err)
		}
		first := firstNames[rng.Intn(len(firstNames))]
		last := lastNames[rng.Intn(len(lastNames))]
		// Sequential username so a single search query like "user"
		// surfaces every seeded row at once.
		username := strings.ToLower(*prefix) + strconv.Itoa(*start+i)
		email := username + "@seed.wakeup.test"
		display := first + " " + last

		_, err = repo.Create(ctx, userrepo.CreateParams{
			ID:           id,
			Username:     username,
			DisplayName:  display,
			Email:        email,
			PasswordHash: hash,
		})
		if err != nil {
			// Username/email collision is the expected idempotent
			// path on re-runs. Anything else is a real problem.
			if isUniqueViolation(err) {
				skipped++
				continue
			}
			fatal(logger, "create user", err)
		}
		created++
		if created%100 == 0 {
			logger.Info("seed: progress", "created", created, "skipped", skipped)
		}
	}
	logger.Info("seed: done", "created", created, "skipped", skipped, "total", *count)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps under fmt.Errorf("%w") — substring match on the
	// SQLSTATE label is good enough for a seed tool.
	msg := err.Error()
	return strings.Contains(msg, "23505") ||
		strings.Contains(strings.ToLower(msg), "duplicate key")
}

func fatal(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "error", err)
	if !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}
