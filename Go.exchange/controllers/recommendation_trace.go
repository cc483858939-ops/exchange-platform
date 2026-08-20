package controllers

import (
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

func buildRecommendationResultTraces(request models.RecommendationRequest, selected []selectedRecommendation, now time.Time, cfg config.RecommendationConfig) []models.RecommendationResultTrace {
	expiresAt := now.AddDate(0, 0, cfg.Trace.ResultRetentionDays)
	result := make([]models.RecommendationResultTrace, 0, len(selected))
	for index, item := range selected {
		breakdown := item.Breakdown
		selectionMode := string(item.SelectionMode)
		if selectionMode == "" {
			selectionMode = string(recommendationResultSelectionRanked)
		}
		result = append(result, models.RecommendationResultTrace{
			RequestID: request.RequestID, Position: index + 1, ArticleID: item.Article.ID, AuthorID: item.Article.AuthorID,
			FromSemantic: item.Candidate.FromSemantic, FromFollowing: item.Candidate.FromFollowing,
			FromRecent: item.Candidate.FromRecent, FromTrending: item.Candidate.FromTrending,
			IsInNetwork: item.IsInNetwork, IsNovelAuthor: item.IsNovelAuthor,
			WasSoftServedFallback: item.Candidate.WasSoftServed,
			PositiveSemantic:      breakdown.PositiveSemantic, NegativeSemantic: breakdown.NegativeSemantic,
			NegativeConfidence: breakdown.NegativeConfidence, InteractionAffinity: breakdown.InteractionAffinity,
			FollowingBonusApplied: breakdown.FollowingBonusApplied, SemanticComponent: breakdown.SemanticComponent,
			TrendingComponent:       breakdown.TrendingComponent,
			AuthorAffinityComponent: breakdown.AuthorAffinityComponent, DiversityPenalty: breakdown.DiversityPenalty,
			BaseScore: breakdown.BaseScore, FinalScore: breakdown.FinalScore,
			ExplorationOpportunity: item.ExplorationOpportunity, SelectionMode: selectionMode,
			ExplorationReason: item.ExplorationReason, ExplorationSemantic: item.ExplorationSemantic,
			CreatedAt: now, ExpiresAt: expiresAt,
		})
	}
	return result
}
