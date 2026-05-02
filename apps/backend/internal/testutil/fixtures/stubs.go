package fixtures

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// Stubs for the rest of the §12.6 fixture surface. Real implementations land
// in their respective phases as the aggregates exist (per milestone 1.9).
// They panic with a clear pointer to the milestone that fills each one in
// so tests that try to use them before the aggregate exists fail loudly.

// MakeFriendship inserts a friendship row. Phase 4.1 (friendship repo).
func MakeFriendship(t *testing.T, _ *pgxpool.Pool, _, _ domain.User, _ ...any) any {
	t.Helper()
	panic("fixtures.MakeFriendship: real impl lands in milestone 4.1 (friendship repo)")
}

// MakeConversation inserts a conversation + members. Phase 5.1 (conversation repo).
func MakeConversation(t *testing.T, _ *pgxpool.Pool, _ []domain.User, _ ...any) any {
	t.Helper()
	panic("fixtures.MakeConversation: real impl lands in milestone 5.1 (conversation repo)")
}

// MakeMessage inserts a message. Phase 6.1 (message repo).
func MakeMessage(t *testing.T, _ *pgxpool.Pool, _ any, _ domain.User, _ ...any) any {
	t.Helper()
	panic("fixtures.MakeMessage: real impl lands in milestone 6.1 (message repo)")
}

// MakeAttachment uploads to the FakeObjectStore + inserts a row. Phase 7.1.
// Takes any to avoid an import cycle on testutil.Harness — Phase 7 will
// switch the parameter to *testutil.Harness once the harness is finalized.
func MakeAttachment(t *testing.T, _ any, _ domain.User, _ ...any) any {
	t.Helper()
	panic("fixtures.MakeAttachment: real impl lands in milestone 7.1 (attachment repo)")
}
