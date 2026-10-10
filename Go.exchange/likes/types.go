package likes

import (
	"errors"
	"time"

	"Go.exchange/config"
)

// ErrNotReady is retained for callers that used the original readiness error.
// New code should inspect ErrUserLikeNotReady or ErrPostLikeNotReady.
var ErrNotReady = errors.New("post like state is not ready")

var (
	ErrUserLikeNotReady               = errors.New("user like state is not ready")
	ErrPostLikeNotReady               = errors.New("post like state is not ready")
	ErrPostLikeUnavailable            = errors.New("post not found")
	ErrLikeProjectionNotReady         = errors.New("post like projection is not ready")
	ErrLikeRecoveryUnsafe             = errors.New("post like state cannot be safely recovered")
	ErrLikeRecoveryFenceLost          = errors.New("post like recovery fence changed")
	ErrLikeRedisType                  = errors.New("unexpected Redis key type")
	ErrUserLikeRedisType              = errors.New("unexpected User Like Redis key type")
	ErrUserLikeCold                   = errors.New("User Like state is legally cold")
	ErrUserLikeRecoveryUnsafe         = errors.New("User Like state cannot be safely recovered")
	ErrUserLikeRecoveryBusy           = errors.New("User Like recovery is already in progress")
	ErrUserLikeRecoveryTimeout        = errors.New("User Like recovery timed out")
	ErrUserLikeRecoveryUnavailable    = errors.New("User Like state is temporarily unavailable")
	ErrUserLikeRecoveryTooLarge       = errors.New("User Like recovery exceeds configured relation limit")
	ErrUserLikeRecoveryDisabled       = errors.New("User Like recovery is disabled")
	ErrUserLikeRecoveryLockLost       = errors.New("User Like recovery lock ownership changed")
	ErrUserLikeRecoveryIncomplete     = errors.New("User Like recovery temporary state is incomplete")
	ErrUserLikeOrderIndexMissing      = errors.New("User Like order index is missing")
	ErrUserLikeOrderIndexInconsistent = errors.New("User Like order index is inconsistent")
	ErrUserLikeCapReached             = errors.New("User Like relation cap reached")
	ErrUserLikeOverCap                = errors.New("User Like relation state exceeds the configured hard cap")
	ErrUserLikeLedgerType             = errors.New("unexpected User Like expiry ledger Redis key type")
	ErrUserLikeRestoreLockType        = errors.New("unexpected User Like restore lock Redis key type")
	ErrPostLikeRedisType              = errors.New("unexpected Post Like Redis key type")
	ErrLikeRelationLifecycleMismatch  = errors.New("deleted Post has active Redis Like state")
	ErrLikeCountInconsistent          = errors.New("post like count is inconsistent with user relation")
	ErrLikeStateExpiryUnsupported     = config.ErrLikeStateExpiryUnsupported
)

type likeRedisTypeError struct{ kind error }

func (e likeRedisTypeError) Error() string { return e.kind.Error() }

func (e likeRedisTypeError) Is(target error) bool {
	return target == ErrLikeRedisType || target == e.kind
}

func userLikeRedisTypeError() error { return likeRedisTypeError{kind: ErrUserLikeRedisType} }

func userLikeLedgerTypeError() error { return likeRedisTypeError{kind: ErrUserLikeLedgerType} }

func postLikeRedisTypeError() error { return likeRedisTypeError{kind: ErrPostLikeRedisType} }

type UserLikeCleanupIssue struct {
	PostID uint
	Kind   string
}

const (
	UserLikeCleanupPostReadyTypeError = "post_ready_type_error"
	UserLikeCleanupLifecycleMismatch  = "post_lifecycle_mismatch"
	UserLikeCleanupUnexpectedReady    = "post_ready_state_invalid"
)

type readinessError struct{ kind error }

func (e readinessError) Error() string { return e.kind.Error() }

func (e readinessError) Is(target error) bool {
	return target == ErrNotReady || target == e.kind
}

func userNotReadyError() error { return readinessError{kind: ErrUserLikeNotReady} }

func postNotReadyError() error { return readinessError{kind: ErrPostLikeNotReady} }

type MutationResult struct {
	Count           int64
	Liked           bool
	Changed         bool
	Version         int64
	EvictedPostID   uint
	ActiveRelations int64
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
