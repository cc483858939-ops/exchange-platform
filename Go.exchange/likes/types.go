package likes

import (
	"errors"
	"time"
)

// ErrNotReady is retained for callers that used the original readiness error.
// New code should inspect ErrUserLikeNotReady or ErrPostLikeNotReady.
var ErrNotReady = errors.New("post like state is not ready")

var (
	ErrUserLikeNotReady           = errors.New("user like state is not ready")
	ErrPostLikeNotReady           = errors.New("post like state is not ready")
	ErrPostLikeUnavailable        = errors.New("post not found")
	ErrLikeProjectionNotReady     = errors.New("post like projection is not ready")
	ErrLikeRecoveryUnsafe         = errors.New("post like state cannot be safely recovered")
	ErrLikeRecoveryFenceLost      = errors.New("post like recovery fence changed")
	ErrLikeRedisType              = errors.New("unexpected Redis key type")
	ErrLikeCountInconsistent      = errors.New("post like count is inconsistent with user relation")
	ErrLikeStateExpiryUnsupported = errors.New("post like state expiry is unsupported while persistent user relations remain")
)

type readinessError struct{ kind error }

func (e readinessError) Error() string { return e.kind.Error() }

func (e readinessError) Is(target error) bool {
	return target == ErrNotReady || target == e.kind
}

func userNotReadyError() error { return readinessError{kind: ErrUserLikeNotReady} }

func postNotReadyError() error { return readinessError{kind: ErrPostLikeNotReady} }

type MutationResult struct {
	Count   int64
	Liked   bool
	Changed bool
	Version int64
}

type State struct {
	Count   int64
	Liked   bool
	Version int64
}

type FullState struct {
	Count   int64
	Version int64
}

type RecoveryFence struct {
	ExpectedVersion    *int64
	AllowZeroBootstrap bool
	// RebuildToken must be acquired before reading the SQL baseline.
	RebuildToken string
}

type SnapshotClaim struct {
	PostID  uint
	ClaimID string
}

type Snapshot struct {
	PostID  uint
	Count   int64
	Version int64
}

type BehaviorClaim struct {
	Pair    string
	ClaimID string
}

type BehaviorDelivery struct {
	Claim      BehaviorClaim
	UserID     uint
	PostID     uint
	Liked      bool
	Version    int64
	OccurredAt time.Time
}
