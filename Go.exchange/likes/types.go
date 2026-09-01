package likes

import (
	"errors"
	"time"
)

var ErrNotReady = errors.New("post like state is not ready")

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
