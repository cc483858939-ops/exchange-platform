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
type recommendationSelectionMode = SelectionPhase
type recommendationRecallSource = CandidateSource
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

func defaultRecommendationConfig() config.RecommendationConfig {
	return config.RecommendationConfig{
		Fusion:      config.RecommendationFusionConfig{RankConstant: 60},
		Trending:    config.RecommendationTrendingConfig{MaxAgeDays: 3, HalfLifeHours: 12, ReplyFactor: 1.5},
		Exploration: config.RecommendationExplorationConfig{Ratio: 0.10, MaxSlots: 3, RecentWindowDays: 7, NovelPostMaxAgeDays: 30},
		LanguageAffinity: config.RecommendationLanguageAffinityConfig{
			Enabled: true, Weight: 0.35, EvidenceSaturationScale: 5, MaxBehaviorShare: 0.95,
		},
		SemanticWeight: 4, NegativeSemanticWeight: 1.5, TrendingWeight: 0.5,
		AuthorAffinityWeight: 1, FollowingBonus: 0.5, OutOfNetworkMinRatio: 0.30,
		Diversity: config.RecommendationDiversityConfig{
			Enabled: true, AuthorWindowSize: 8, MaxSameAuthorInWindow: 2,
			SemanticDuplicateThreshold: 0.92, SemanticDuplicatePenalty: 1,
		},
	}
}

func normalizedRecommendationConfig() config.RecommendationConfig {
	return defaultRecommendationConfig()
}

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

func recommendationExplorationSemantic(candidate RankedCandidate, profile ProfileFeatures, cfg config.RecommendationConfig) float64 {
	return explorationSemantic(candidate, profile, testRankingConfig(cfg))
}

func recommendationTrendingRaw(post models.Post, now time.Time, cfg config.RecommendationConfig) float64 {
	return TrendingRaw(post, now, testRankingConfig(cfg).Trending)
}

func recommendationCandidateBaseBefore(left, right RankedCandidate) bool {
	return rankedCandidateBefore(left, right)
}

func validComparableRecommendationEmbedding(left, right []float32) bool {
	return validComparableEmbedding(left, right)
}

func fuseRecommendationCandidates(limit, rankConstant int, lists ...CandidateSet) []Candidate {
	return FuseCandidates(limit, FusionConfig{RankConstant: rankConstant}, lists...)
}

func recommendationFusionCandidateBefore(left, right Candidate) bool {
	return fusionCandidateBefore(left, right)
}

func recommendationBestRecallRank(candidate Candidate) int {
	return bestRecallRank(candidate)
}

func recommendationMinNonZeroRank(left, right int) int {
	return minNonZeroRank(left, right)
}

func mergeEmbeddingCandidates(limit int, sources ...[]Candidate) []Candidate {
	return MergeCandidates(limit, sources...)
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

func recommendationAuthorWindowAllows(candidate RankedCandidate, selected []SelectedCandidate, cfg config.RecommendationConfig) bool {
	return authorWindowAllows(candidate, selected, testSelectionConfig(cfg).Diversity)
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

func recommendationExplorationReasonPriority(reason ExplorationReason) int {
	return explorationReasonPriority(reason)
}

func recommendationStrictExplorationBefore(left, right RankedCandidate, now time.Time, cfg config.RecommendationConfig) bool {
	return strictExplorationBefore(left, right, now, testSelectionConfig(cfg))
}

func recommendationSelectionBefore(left, right RankedCandidate, phase SelectionPhase) bool {
	return selectionBefore(left, right, phase)
}

func diversifyPublicRecommendationCandidates(ranked []RankedCandidate, limit int, requestID string) []RankedCandidate {
	return DiversifyPublicCandidates(ranked, limit, requestID)
}

func buildRecommendationLanguageContext(browser LanguageContext, behavior LanguagePrior, evidence float64, cfg config.RecommendationConfig) LanguageContext {
	return BuildLanguageContext(browser, behavior, evidence, testRankingConfig(cfg).Language)
}

func normalizedMaterializedRecommendationLanguagePrior(zh, ja, en, evidence float64) (LanguagePrior, float64) {
	return NormalizeMaterializedLanguagePrior(zh, ja, en, evidence)
}

func normalizeRecommendationLanguagePrior(prior LanguagePrior) LanguagePrior {
	return NormalizeLanguagePrior(prior)
}

func recommendationLanguagePriorPresent(prior LanguagePrior) bool {
	return languagePriorPresent(prior)
}

func recommendationLanguageAffinityForPost(language string, context LanguageContext) float64 {
	return LanguageAffinity(language, context.Combined)
}

func recommendationPostLanguage(language string) string {
	return PostLanguage(language)
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
