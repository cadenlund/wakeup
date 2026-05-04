package notification

import (
	"context"

	"github.com/google/uuid"
)

// NeverSuppress is a SuppressionChecker that never suppresses,
// regardless of input. Equivalent to passing nil Suppression in
// Config; exported so tests in other packages can opt out of
// suppression explicitly without re-defining the type.
//
// Production wires the repository-backed implementation from
// `internal/repository/notification`.
var NeverSuppress SuppressionChecker = neverSuppress{}

type neverSuppress struct{}

func (neverSuppress) PushSuppressed(context.Context, uuid.UUID, *uuid.UUID) (bool, error) {
	return false, nil
}
