package recommendation

import (
	"time"

	"Go.exchange/models"
)

type CandidateSource uint8

const (
	CandidateSourceSemantic CandidateSource = iota + 1
	CandidateSourceFollowing
	CandidateSourceRecent
	CandidateSourceTrending
)

type Candidate struct {
	PostID                     uint
	PositiveSemanticSimilarity float64
	SemanticRank               int
	FollowingRank              int
	RecentRank                 int
	TrendingRank               int
	FusionScore                float64
	SourceCount                int
	FromSemantic               bool
	FromFollowing              bool
	FromRecent                 bool
	FromTrending               bool
	WasSoftServed              bool
	LastServedAt               time.Time
}

type CandidateSet struct {
	Source     CandidateSource
	Candidates []Candidate
}

type ScoreBreakdown struct {
	PositiveSemantic        float64
	NegativeSemantic        float64
	NegativeConfidence      float64
	InteractionAffinity     float64
	FollowingBonusApplied   float64
	SemanticComponent       float64
	TrendingComponent       float64
	AuthorAffinityComponent float64
	LanguageAffinity        float64
	LanguageComponent       float64
	DiversityPenalty        float64
	BaseScore               float64
	FinalScore              float64
}

type ProfileFeatures struct {
	PositiveVector     []float32
	NegativeVector     []float32
	NegativeConfidence float64
	AuthorAffinity     map[uint]float64
	FollowingAuthorIDs map[uint]struct{}
}

// RankedCandidate carries only data consumed by ranking and selection. Post
// remains a temporary model-backed input while the serving hydration boundary
// is narrowed in a later migration stage.
type RankedCandidate struct {
	Candidate           Candidate
	Post                models.Post
	Embedding           []float32
	Breakdown           ScoreBreakdown
	ExplorationSemantic float64
	IsInNetwork         bool
	IsNovelAuthor       bool
}

type SelectionMode string

const (
	SelectionModeRanked      SelectionMode = "ranked"
	SelectionModeExploration SelectionMode = "exploration"
)

type ExplorationReason string

const (
	ExplorationReasonRecent            = "recent"
	ExplorationReasonNovelAuthor       = "novel_author"
	ExplorationReasonRecentNovelAuthor = "recent_novel_author"
)

type SelectedCandidate struct {
	Candidate              Candidate
	Post                   models.Post
	Embedding              []float32
	Breakdown              ScoreBreakdown
	IsInNetwork            bool
	IsNovelAuthor          bool
	ExplorationOpportunity bool
	SelectionMode          SelectionMode
	ExplorationReason      ExplorationReason
	ExplorationSemantic    float64
}

type SelectionPhase uint8

const (
	SelectionPhaseFresh SelectionPhase = iota
	SelectionPhaseSoft
)

type TrendingConfig struct {
	MaxAgeDays    int
	HalfLifeHours float64
	ReplyFactor   float64
}

type LanguageConfig struct {
	Enabled                 bool
	Weight                  float64
	EvidenceSaturationScale float64
	MaxBehaviorShare        float64
}

type RankingConfig struct {
	SemanticWeight       float64
	NegativeSemanticWeight float64
	TrendingWeight       float64
	AuthorAffinityWeight float64
	FollowingBonus       float64
	Trending             TrendingConfig
	Language             LanguageConfig
}

type FusionConfig struct {
	RankConstant int
}

type DiversityConfig struct {
	Enabled                    bool
	AuthorWindowSize           int
	MaxSameAuthorInWindow      int
	SemanticDuplicateThreshold float64
	SemanticDuplicatePenalty  float64
}

type ExplorationConfig struct {
	Ratio               float64
	MaxSlots            int
	RecentWindowDays    int
	NovelPostMaxAgeDays int
}

type SelectionConfig struct {
	OutOfNetworkMinRatio float64
	Diversity            DiversityConfig
	Exploration          ExplorationConfig
}

type SelectionInput struct {
	Candidates []RankedCandidate
	Initial    []SelectedCandidate
	Limit      int
	Now        time.Time
	Phase      SelectionPhase
	RequestID  string
	Config     SelectionConfig
}

type SelectionCounts struct {
	Target        int
	Opportunities int
	Results       int
}

type LanguagePrior struct {
	ZH float64
	JA float64
	EN float64
}

type LanguageContext struct {
	Browser          LanguagePrior
	Behavior         LanguagePrior
	Combined         LanguagePrior
	BrowserPrimary   string
	BehaviorEvidence float64
	BehaviorShare    float64
	Source           string
}

const (
	LanguageZH  = "zh"
	LanguageJA  = "ja"
	LanguageEN  = "en"
	LanguageUnd = "und"
	RankerVersion = "rules_v6"
	SelectionPolicyVersion = "network_balance_exploration_v2"
)
