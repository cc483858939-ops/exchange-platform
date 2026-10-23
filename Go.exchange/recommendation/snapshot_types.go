package recommendation

import (
	"context"
	"time"
)

const candidatePoolSnapshotVersion = 5

type CachedCandidate struct {
	Candidate              Candidate         `json:"candidate"`
	RecallTier             RecallTier        `json:"recall_tier"`
	PostID                 uint              `json:"post_id"`
	AuthorID               uint              `json:"author_id"`
	CreatedAt              time.Time         `json:"created_at"`
	Breakdown              ScoreBreakdown    `json:"breakdown"`
	ExplorationSemantic    float64           `json:"exploration_semantic"`
	IsInNetwork            bool              `json:"is_in_network"`
	IsNovelAuthor          bool              `json:"is_novel_author"`
	Phase                  SelectionPhase    `json:"phase"`
	SelectionMode          SelectionMode     `json:"selection_mode,omitempty"`
	ExplorationReason      ExplorationReason `json:"exploration_reason,omitempty"`
	ExplorationOpportunity bool              `json:"exploration_opportunity,omitempty"`
}

type CachedPage struct {
	Index               int               `json:"index"`
	RequestID           string            `json:"request_id"`
	ServedAt            time.Time         `json:"served_at"`
	GenerationLatencyMS int64             `json:"generation_latency_ms"`
	Items               []CachedCandidate `json:"items"`
	ReturnedPostIDs     []uint            `json:"returned_post_ids,omitempty"`
	// Committed freezes the mapper-approved IDs before any page side effect.
	Committed bool `json:"committed,omitempty"`
	// HistoryRecorded means the page-keyed history receipt was confirmed.
	HistoryRecorded bool `json:"history_recorded,omitempty"`
	// TraceAttempted is claimed before enqueue. A process crash between the
	// claim and enqueue can lose a trace; trace delivery remains best-effort.
	TraceAttempted bool `json:"trace_attempted,omitempty"`
	// Finalized means history and trace side-effect attempts are complete; it
	// does not imply successful asynchronous trace persistence.
	Finalized bool `json:"finalized"`
}

type CandidatePoolSnapshot struct {
	Version                     int               `json:"version"`
	ID                          string            `json:"id"`
	OwnerDigest                 string            `json:"owner_digest"`
	CreatedAt                   time.Time         `json:"created_at"`
	RecentCutoff                time.Time         `json:"recent_cutoff"`
	ExpiresAt                   time.Time         `json:"expires_at"`
	ExpiresAtUnixMilli          int64             `json:"expires_at_unix_milli"`
	PageSize                    int               `json:"page_size"`
	EmbeddingVersion            string            `json:"embedding_version"`
	RankerConfigHash            string            `json:"ranker_config_hash"`
	ProfileComputedAt           time.Time         `json:"profile_computed_at"`
	LanguageKey                 string            `json:"language_key"`
	StrategyVersion             string            `json:"strategy_version"`
	Candidates                  []CachedCandidate `json:"-"`
	HistoricalCursor            *HistoricalCursor `json:"historical_cursor,omitempty"`
	HistoricalExhausted         bool              `json:"historical_exhausted"`
	HistoricalBudgetLimited     bool              `json:"historical_budget_limited,omitempty"`
	HistoricalCandidatesScanned int               `json:"historical_candidates_scanned,omitempty"`
	HistoricalBatches           int               `json:"historical_batches,omitempty"`
	OldestHistoricalCreatedAt   time.Time         `json:"oldest_historical_created_at,omitempty"`
	SoftCursor                  *HistoricalCursor `json:"soft_cursor,omitempty"`
	SoftExhausted               bool              `json:"soft_exhausted"`
	PageCount                   int               `json:"page_count"`
	Exhausted                   bool              `json:"exhausted"`
	Revision                    int64             `json:"revision"`
	// These aggregate views are used only by in-memory test stores. The Redis
	// Hash codec never persists or loads them; serving paths use page-indexed
	// store operations and exact post-state lookups instead.
	UsedPostIDs           []uint       `json:"-"`
	MapperRejectedPostIDs []uint       `json:"-"`
	Pages                 []CachedPage `json:"-"`
	DiscardedPostIDs      []uint       `json:"-"`
}

// SnapshotContextEntry is a compact, bounded ranking context. It stores only
// the candidates required by the configured author and semantic windows.
type SnapshotContextEntry struct {
	PageIndex int             `json:"page_index"`
	Candidate CachedCandidate `json:"candidate"`
}

type SnapshotPostStateUpdate struct {
	PostID   uint
	Expected string
	Next     string
}

type SnapshotPendingHistoryChange int8

const (
	SnapshotPendingHistoryUnchanged SnapshotPendingHistoryChange = 0
	SnapshotPendingHistoryAdd       SnapshotPendingHistoryChange = 1
	SnapshotPendingHistoryRemove    SnapshotPendingHistoryChange = -1
)

// SnapshotMutation atomically changes the bounded pool/meta fields, at most
// one page record, exact per-post lifecycle fields, and the bounded context.
type SnapshotMutation struct {
	Snapshot             CandidatePoolSnapshot
	Page                 *CachedPage
	DeletePageIndices    []int
	PostStates           []SnapshotPostStateUpdate
	Context              *[]SnapshotContextEntry
	PendingHistoryChange SnapshotPendingHistoryChange
	PendingHistoryPage   int
}

type SnapshotStore interface {
	Create(ctx context.Context, snapshot CandidatePoolSnapshot, pages []CachedPage, context []SnapshotContextEntry, ttl time.Duration) error
	Load(ctx context.Context, snapshotID string) (CandidatePoolSnapshot, error)
	LoadPage(ctx context.Context, snapshotID string, pageIndex int) (CachedPage, bool, error)
	LoadContext(ctx context.Context, snapshotID string, beforePageIndex int) ([]SnapshotContextEntry, error)
	LoadPendingHistory(ctx context.Context, snapshotID string, limit int) ([]CachedPage, error)
	KnownPostIDs(ctx context.Context, snapshotID string, postIDs []uint) (map[uint]struct{}, error)
	Apply(ctx context.Context, snapshotID string, expectedRevision int64, mutation SnapshotMutation) (bool, error)
}

type SnapshotCandidateRepository interface {
	LoadEligibleSnapshotCandidates(ctx context.Context, query CandidateQuery, candidates []Candidate) ([]RankedCandidate, error)
}

type SnapshotPageRepository interface {
	LoadSnapshotPage(ctx context.Context, query CandidateQuery, candidates []Candidate, contextPostIDs []uint) ([]RankedCandidate, map[uint][]float32, error)
}

type servingFinalization struct {
	snapshotID         string
	pageIndex          int
	request            ServeRequest
	generationDuration time.Duration
	traceContext       context.Context
}
