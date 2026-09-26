package recommendation

import (
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

func buildRecommendationRequest(request ServeRequest, result ServeResult, started time.Time, cfg config.RecommendationConfig) models.RecommendationRequest {
	selected := result.Selected
	candidates := result.FreshCandidateSummary
	profile := result.Profile
	exploration := ExplorationCountsForSelection(selected, ExplorationTarget(request.Limit, selectionConfig(cfg).Exploration))
	return models.RecommendationRequest{
		RequestID: request.RequestID, UserID: request.Viewer.UserID, Scene: recommendationScene, StrategyID: result.StrategyID,
		RankerVersion: RankerVersion, RankerConfigHash: result.RankerConfigHash,
		ProfileVersion: profile.ProfileVersion, ProfileConfigHash: profile.ProfileConfigHash,
		ProfileStatus: profile.ProfileStatus, ProfileAgeMS: profile.ProfileAgeMS,
		BrowserLanguagePrimary: result.LanguageContext.BrowserPrimary, LanguageContextSource: result.LanguageContext.Source,
		LanguageBehaviorEvidence: result.LanguageContext.BehaviorEvidence, LanguageBehaviorShare: result.LanguageContext.BehaviorShare,
		LanguageAffinityZH: result.LanguageContext.Combined.ZH, LanguageAffinityJA: result.LanguageContext.Combined.JA,
		LanguageAffinityEN: result.LanguageContext.Combined.EN,
		RequestedLimit:     request.Limit, CandidateCount: len(candidates.Candidates), ResultCount: len(selected),
		TrackedResultCount: len(result.Tracking), PersonalizedSignalCount: profile.PersonalizedSignalCount,
		SemanticCandidateCount: candidates.SemanticCount, FollowingCandidateCount: candidates.FollowingCount,
		RecentCandidateCount: candidates.RecentCount, TrendingCandidateCount: candidates.TrendingCount,
		MergedCandidateCount: len(candidates.Candidates), PositiveSignalCount: profile.PositiveSignalCount,
		NegativeSignalCount:     profile.NegativeSignalCount,
		InNetworkResultCount:    countSelected(selected, func(item SelectedCandidate) bool { return item.IsInNetwork }),
		OutOfNetworkResultCount: countSelected(selected, func(item SelectedCandidate) bool { return !item.IsInNetwork }),
		NovelAuthorResultCount:  countSelected(selected, func(item SelectedCandidate) bool { return item.IsNovelAuthor }),
		ExplorationTargetCount:  exploration.Target, ExplorationOpportunityCount: exploration.Opportunities,
		ExplorationResultCount:  exploration.Results,
		SoftServedFallbackCount: countSelected(selected, func(item SelectedCandidate) bool { return item.Candidate.WasSoftServed }),
		PersonalizationMode:     result.PersonalizationMode, FallbackReason: result.FallbackReason,
		GenerationLatencyMS: time.Since(started).Milliseconds(), CreatedAt: request.Now,
	}
}

func buildResultTraces(request models.RecommendationRequest, selected []SelectedCandidate, now time.Time, cfg config.RecommendationConfig) []models.RecommendationResultTrace {
	expiresAt := now.AddDate(0, 0, cfg.Trace.ResultRetentionDays)
	result := make([]models.RecommendationResultTrace, 0, len(selected))
	for index, item := range selected {
		breakdown := item.Breakdown
		selectionMode := string(item.SelectionMode)
		if selectionMode == "" {
			selectionMode = string(SelectionModeRanked)
		}
		result = append(result, models.RecommendationResultTrace{
			RequestID: request.RequestID, Position: index + 1, PostID: item.Post.ID, AuthorID: item.Post.AuthorID,
			FromSemantic: item.Candidate.FromSemantic, FromFollowing: item.Candidate.FromFollowing,
			FromRecent: item.Candidate.FromRecent, FromTrending: item.Candidate.FromTrending,
			FusionScore: item.Candidate.FusionScore, SourceCount: item.Candidate.SourceCount,
			SemanticRank: item.Candidate.SemanticRank, FollowingRank: item.Candidate.FollowingRank,
			RecentRank: item.Candidate.RecentRank, TrendingRank: item.Candidate.TrendingRank,
			PostLanguage:     PostLanguage(item.Post.Language),
			LanguageAffinity: ClampUnit(breakdown.LanguageAffinity), LanguageComponent: NonNegativeScore(breakdown.LanguageComponent),
			IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			WasSoftServedFallback: item.Candidate.WasSoftServed,
			PositiveSemantic:      breakdown.PositiveSemantic, NegativeSemantic: breakdown.NegativeSemantic,
			NegativeConfidence: breakdown.NegativeConfidence, InteractionAffinity: breakdown.InteractionAffinity,
			FollowingBonusApplied: breakdown.FollowingBonusApplied, SemanticComponent: breakdown.SemanticComponent,
			TrendingComponent:       breakdown.TrendingComponent,
			AuthorAffinityComponent: breakdown.AuthorAffinityComponent, DiversityPenalty: breakdown.DiversityPenalty,
			BaseScore: breakdown.BaseScore, FinalScore: breakdown.FinalScore,
			ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: selectionMode,
			ExplorationReason: string(item.ExplorationReason), ExplorationSemantic: item.ExplorationSemantic,
			CreatedAt: now, ExpiresAt: expiresAt,
		})
	}
	return result
}

func countSelected(items []SelectedCandidate, predicate func(SelectedCandidate) bool) int {
	count := 0
	for _, item := range items {
		if predicate(item) {
			count++
		}
	}
	return count
}
