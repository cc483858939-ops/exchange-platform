package recommendation

import (
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

// These aliases and adapters keep the existing controller-era golden tests
// readable while exercising the extracted domain implementation directly.
type embeddingCandidate = Candidate
type hydratedRecommendationCandidate = RankedCandidate
type selectedRecommendation = SelectedCandidate
type recommendationScoreBreakdown = ScoreBreakdown
type userInterestProfile = ProfileFeatures
type recommendationLanguagePrior = LanguagePrior
type recommendationLanguageContext = LanguageContext
type recommendationRecallList = CandidateSet

const (
	recommendationSelectionFresh = SelectionPhaseFresh
	recommendationSelectionSoft  = SelectionPhaseSoft

	recommendationResultSelectionRanked              = SelectionModeRanked
	recommendationResultSelectionExploration         = SelectionModeExploration
	recommendationExplorationReasonRecent            = ExplorationReasonRecent
	recommendationExplorationReasonNovelAuthor       = ExplorationReasonNovelAuthor
	recommendationExplorationReasonRecentNovelAuthor = ExplorationReasonRecentNovelAuthor

	recommendationRecallSourceSemantic  = CandidateSourceSemantic
	recommendationRecallSourceFollowing = CandidateSourceFollowing
	recommendationRecallSourceRecent    = CandidateSourceRecent
	recommendationRecallSourceTrending  = CandidateSourceTrending
)

func testRankingConfig(cfg config.RecommendationConfig) RankingConfig {
	return RankingConfig{
		SemanticWeight: cfg.SemanticWeight, NegativeSemanticWeight: cfg.NegativeSemanticWeight,
		TrendingWeight: cfg.TrendingWeight, AuthorAffinityWeight: cfg.AuthorAffinityWeight,
		FollowingBonus: cfg.FollowingBonus,
		Trending: TrendingConfig{
			MaxAgeDays: cfg.Trending.MaxAgeDays, HalfLifeHours: cfg.Trending.HalfLifeHours,
			ReplyFactor: cfg.Trending.ReplyFactor,
		},
		Language: LanguageConfig{
			Enabled: cfg.LanguageAffinity.Enabled, Weight: cfg.LanguageAffinity.Weight,
			EvidenceSaturationScale: cfg.LanguageAffinity.EvidenceSaturationScale,
			MaxBehaviorShare:        cfg.LanguageAffinity.MaxBehaviorShare,
		},
	}
}

func testSelectionConfig(cfg config.RecommendationConfig) SelectionConfig {
	return SelectionConfig{
		OutOfNetworkMinRatio: cfg.OutOfNetworkMinRatio,
		Diversity: DiversityConfig{
			Enabled: cfg.Diversity.Enabled, AuthorWindowSize: cfg.Diversity.AuthorWindowSize,
			MaxSameAuthorInWindow:      cfg.Diversity.MaxSameAuthorInWindow,
			SemanticDuplicateThreshold: cfg.Diversity.SemanticDuplicateThreshold,
			SemanticDuplicatePenalty:   cfg.Diversity.SemanticDuplicatePenalty,
		},
		Exploration: ExplorationConfig{
			Ratio: cfg.Exploration.Ratio, MaxSlots: cfg.Exploration.MaxSlots,
			RecentWindowDays:    cfg.Exploration.RecentWindowDays,
			NovelPostMaxAgeDays: cfg.Exploration.NovelPostMaxAgeDays,
		},
	}
}

func rankRecommendationCandidates(profile ProfileFeatures, candidates []RankedCandidate, now time.Time, cfg config.RecommendationConfig, languages ...LanguageContext) []RankedCandidate {
	return RankCandidates(profile, candidates, now, testRankingConfig(cfg), languages...)
}

func recommendationTrendingRaw(post models.Post, now time.Time, cfg config.RecommendationConfig) float64 {
	return TrendingRaw(post, now, testRankingConfig(cfg).Trending)
}

func fuseRecommendationCandidates(limit, rankConstant int, lists ...CandidateSet) []Candidate {
	return FuseCandidates(limit, FusionConfig{RankConstant: rankConstant}, lists...)
}

func recommendationFusionCandidateBefore(left, right Candidate) bool {
	return fusionCandidateBefore(left, right)
}

func balancedPositions(limit, target int) []int {
	return BalancedPositions(limit, target)
}

func recommendationExplorationTarget(limit int, cfg config.RecommendationConfig) int {
	return ExplorationTarget(limit, testSelectionConfig(cfg).Exploration)
}

func selectRecommendationCandidates(candidates []RankedCandidate, initial []SelectedCandidate, limit int, cfg config.RecommendationConfig, now time.Time, phase SelectionPhase, requestIDs ...string) []SelectedCandidate {
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = requestIDs[0]
	}
	return SelectCandidates(SelectionInput{
		Candidates: candidates, Initial: initial, Limit: limit, Now: now, Phase: phase,
		RequestID: requestID, Config: testSelectionConfig(cfg),
	})
}

func chooseStrictExplorationCandidate(candidates []RankedCandidate, selected []SelectedCandidate, available func(RankedCandidate) bool, outPositions map[int]struct{}, position int, cfg config.RecommendationConfig, now time.Time) (int, RankedCandidate, bool) {
	return chooseStrictExplorationCandidateWithConfig(candidates, selected, available, outPositions, position, testSelectionConfig(cfg), now)
}

func recommendationDiversityPenalty(candidate RankedCandidate, selected []SelectedCandidate, cfg config.RecommendationConfig) float64 {
	return diversityPenalty(candidate, selected, testSelectionConfig(cfg).Diversity)
}

func recommendationIsSemanticDuplicate(candidate RankedCandidate, selected []SelectedCandidate, cfg config.RecommendationConfig) bool {
	return isSemanticDuplicate(candidate, selected, testSelectionConfig(cfg).Diversity)
}

func recommendationExplorationReason(candidate RankedCandidate, now time.Time, cfg config.RecommendationConfig) ExplorationReason {
	return explorationReason(candidate, now, testSelectionConfig(cfg).Exploration)
}

func recommendationExplorationCountsForSelection(selected []SelectedCandidate, target int) SelectionCounts {
	return ExplorationCountsForSelection(selected, target)
}

func countSelectedClass(items []SelectedCandidate, predicate func(SelectedCandidate) bool) int {
	count := 0
	for _, item := range items {
		if predicate(item) {
			count++
		}
	}
	return count
}
