package likes

import (
	"errors"
	"time"
)

var ErrNotReady = errors.New("post like state is not ready")

var (
	ErrPostLikeUnavailable    = errors.New("post not found")
	ErrLikeProjectionNotReady = errors.New("post like projection is not ready")
	ErrLikeRecoveryUnsafe     = errors.New("post like state cannot be safely recovered")
	ErrLikeRecoveryFenceLost  = errors.New("post like recovery fence changed")
	ErrLikeRedisType          = errors.New("unexpected Redis key type")
)

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
	UserIDs []uint
}

type RecoveryFence struct {
	ExpectedVersion    *int64
	AllowZeroBootstrap bool
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
