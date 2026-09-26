package recommendation

import (
	"context"
	"time"

	"Go.exchange/models"
)

// CandidateQuery contains already-resolved serving inputs. Repositories only
// apply these bounds and exclusions; callers own config and time policy.
type CandidateQuery struct {
	UserID                        uint
	ServingVersion                string
	Now                           time.Time
	Limit                         int
	PositiveVector                []float32
	SemanticRecentRatio           float64
	SemanticRecentCutoff          time.Time
	TrendingCutoff                time.Time
	TrendingReplyFactor           float64
	TrendingHalfLifeHours         float64
	Served                        map[uint]ServedItem
	SoftOnly                      bool
	MaterializedInteractionsReady bool
	InteractedPostIDs             map[uint]struct{}
}

type PublicCandidateQuery struct {
	Now                   time.Time
	Limit                 int
	TrendingCutoff        time.Time
	TrendingReplyFactor   float64
	TrendingHalfLifeHours float64
	ExcludedPostIDs       map[uint]struct{}
}

type CandidateRepository interface {
	LoadSemanticCandidates(ctx context.Context, query CandidateQuery) ([]Candidate, error)
	LoadFollowingCandidates(ctx context.Context, query CandidateQuery) ([]Candidate, error)
	LoadRecentCandidates(ctx context.Context, query CandidateQuery) ([]Candidate, error)
	LoadTrendingCandidates(ctx context.Context, query CandidateQuery) ([]Candidate, error)
	LoadPublicRecentCandidates(ctx context.Context, query PublicCandidateQuery) ([]Candidate, error)
	LoadPublicTrendingCandidates(ctx context.Context, query PublicCandidateQuery) ([]Candidate, error)
	HydrateCandidates(ctx context.Context, servingVersion string, candidates []Candidate, now time.Time) ([]RankedCandidate, error)
	LoadPostEmbeddings(ctx context.Context, postIDs []uint, version string) (map[uint][]float32, error)
}

type Profile struct {
	PositiveVector                []float32
	NegativeVector                []float32
	NegativeConfidence            float64
	InteractedPostIDs             map[uint]struct{}
	PositiveSignalCount           int
	NegativeSignalCount           int
	PersonalizedSignalCount       int
	LanguageZHWeight              float64
	LanguageJAWeight              float64
	LanguageENWeight              float64
	LanguageEvidence              float64
	PositiveContributions         map[uint]float64
	PositiveAffinityContributions map[uint]float64
	AuthorAffinity                map[uint]float64
	FollowingAuthorIDs            map[uint]struct{}
	ProfileVersion                string
	ProfileConfigHash             string
	ProfileStatus                 string
	ProfileAgeMS                  int64
	MaterializedInteractionsReady bool
}

func (profile Profile) PositiveContributionIDs() []uint {
	ids := make([]uint, 0, len(profile.PositiveContributions))
	for postID := range profile.PositiveContributions {
		ids = append(ids, postID)
	}
	return ids
}

func (profile Profile) PositiveAffinityContributionIDs() []uint {
	ids := make([]uint, 0, len(profile.PositiveAffinityContributions))
	for postID := range profile.PositiveAffinityContributions {
		ids = append(ids, postID)
	}
	return ids
}

const (
	ProfileStatusHit          = "hit"
	ProfileStatusStale        = "stale"
	ProfileStatusMiss         = "miss"
	ProfileStatusIncompatible = "incompatible"
)

type ProfileLoadQuery struct {
	UserID                         uint
	EmbeddingVersion               string
	ExpectedProfileVersion         string
	ExpectedProfileConfigHash      string
	Now                            time.Time
	NegativeConfidenceHalfLifeDays float64
	NegativeConfidenceSaturation   float64
}

type ProfileLoadResult struct {
	Profile        Profile
	RecoveryReason string
	RecoveryError  error
}

type AuthorContextQuery struct {
	UserID                  uint
	AuthorIDs               []uint
	LoadAffinity            bool
	AffinitySaturationScale float64
}

type AuthorContext struct {
	Affinity           map[uint]float64
	FollowingAuthorIDs map[uint]struct{}
}

type PostAffinityInput struct {
	PostID   uint
	AuthorID uint
	Language string
}

type ProfileRepository interface {
	Load(ctx context.Context, query ProfileLoadQuery) (ProfileLoadResult, error)
	LoadAuthorContext(ctx context.Context, query AuthorContextQuery) (AuthorContext, error)
	LoadPostAffinityInputs(ctx context.Context, postIDs []uint) ([]PostAffinityInput, error)
}

type HistoryWindow struct {
	Now       time.Time
	HardStart time.Time
	SoftStart time.Time
	Limit     int
	TTL       time.Duration
}

type ServedItem struct {
	PostID       uint
	LastServedAt time.Time
	Hard         bool
	Soft         bool
}

type ServedHistory map[uint]ServedItem

type HistoryStore interface {
	LoadUserHistory(ctx context.Context, userID uint, window HistoryWindow) (ServedHistory, error)
	RecordUserServed(ctx context.Context, userID uint, postIDs []uint, window HistoryWindow) error
	LoadGuestHistory(ctx context.Context, sessionID string, window HistoryWindow) (ServedHistory, error)
	RecordGuestServed(ctx context.Context, sessionID string, postIDs []uint, window HistoryWindow) error
}

type TraceRepository interface {
	PersistServing(ctx context.Context, request models.RecommendationRequest, results []models.RecommendationResultTrace) error
}

type DataDependencies struct {
	Candidates CandidateRepository
	Profiles   ProfileRepository
	History    HistoryStore
	Traces     TraceRepository
}

type SourceRepository interface {
	LoadSourceSignals(ctx context.Context, userID uint, lookbackStart time.Time) (SourceSignals, error)
}

type DirtyProfileRepository interface {
	InvalidateProfiles(ctx context.Context, userIDs []uint, reason string, now time.Time) error
	EnsureProfilesQueued(ctx context.Context, userIDs []uint, reason string, now time.Time) error
	ListDue(ctx context.Context, cutoff, now time.Time, limit int) ([]DirtyProfile, error)
	Load(ctx context.Context, userID uint) (DirtyProfile, error)
	DeleteClaim(ctx context.Context, userID uint, dirtyVersion int64) (int64, error)
	RetryClaim(ctx context.Context, claim DirtyProfile, cause error, now time.Time) error
}
