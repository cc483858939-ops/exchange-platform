package recommendation

import (
	"context"
	"time"
)

const candidatePoolSnapshotVersion = 4

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
	PageSize                    int               `json:"page_size"`
	EmbeddingVersion            string            `json:"embedding_version"`
	RankerConfigHash            string            `json:"ranker_config_hash"`
	ProfileComputedAt           time.Time         `json:"profile_computed_at"`
	LanguageKey                 string            `json:"language_key"`
	StrategyVersion             string            `json:"strategy_version"`
	Candidates                  []CachedCandidate `json:"candidates"`
	HistoricalCursor            *HistoricalCursor `json:"historical_cursor,omitempty"`
	HistoricalExhausted         bool              `json:"historical_exhausted"`
	HistoricalBudgetLimited     bool              `json:"historical_budget_limited,omitempty"`
	HistoricalCandidatesScanned int               `json:"historical_candidates_scanned,omitempty"`
	HistoricalBatches           int               `json:"historical_batches,omitempty"`
	OldestHistoricalCreatedAt   time.Time         `json:"oldest_historical_created_at,omitempty"`
	SoftCursor                  *HistoricalCursor `json:"soft_cursor,omitempty"`
	SoftExhausted               bool              `json:"soft_exhausted"`
	UsedPostIDs                 []uint            `json:"used_post_ids"`
	// MapperRejectedPostIDs are excluded for this short-lived candidate pool,
	// but are not served-history entries and do not contribute to diversity context.
	MapperRejectedPostIDs []uint       `json:"mapper_rejected_post_ids,omitempty"`
	Pages                 []CachedPage `json:"pages"`
	Exhausted             bool         `json:"exhausted"`
	Revision              int64        `json:"revision"`
}

type SnapshotStore interface {
	Create(ctx context.Context, snapshot CandidatePoolSnapshot, ttl time.Duration, maxBytes int) error
	Load(ctx context.Context, snapshotID string) (CandidatePoolSnapshot, error)
	CompareAndSwap(ctx context.Context, snapshotID string, expectedRevision int64, snapshot CandidatePoolSnapshot, maxBytes int) (bool, error)
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
