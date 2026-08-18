package controllers

import (
	"math"
	"sort"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

type recommendationScoreBreakdown struct {
	PositiveSemantic        float64
	NegativeSemantic        float64
	NegativeConfidence      float64
	InteractionAffinity     float64
	FollowingBonusApplied   float64
	SemanticComponent       float64
	FreshnessComponent      float64
	PopularityComponent     float64
	AuthorAffinityComponent float64
	DiversityPenalty        float64
	BaseScore               float64
	FinalScore              float64
}

func rankRecommendationCandidates(profile userInterestProfile, candidates []hydratedRecommendationCandidate, now time.Time, cfg config.RecommendationConfig) []hydratedRecommendationCandidate {
	for index := range candidates {
		candidate := &candidates[index]
		positiveSemantic := 0.0
		if candidate.Candidate.FromSemantic {
			positiveSemantic = clampRecommendationSimilarity(candidate.Candidate.SemanticSimilarity)
		}
		negativeSemantic := cosineSimilarity(candidate.Embedding, profile.NegativeVector)
		if negativeSemantic < 0 {
			negativeSemantic = 0
		}
		negativeConfidence := profile.NegativeConfidence
		semanticRaw := positiveSemantic - cfg.NegativeSemanticWeight*negativeConfidence*negativeSemantic
		articleTime := candidate.Article.CreatedAt
		if candidate.Article.PublishedAt != nil && !candidate.Article.PublishedAt.IsZero() {
			articleTime = candidate.Article.PublishedAt.UTC()
		}
		ageDays := now.Sub(articleTime).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		freshness := math.Exp(-math.Ln2 * ageDays / cfg.FreshnessHalfLifeDays)
		popularity := math.Log1p(math.Max(0, float64(candidate.Article.LikeCount))) +
			cfg.PopularityCommentFactor*math.Log1p(math.Max(0, float64(candidate.Article.CommentCount)))
		interactionAffinity := clampUnit(profile.AuthorAffinity[candidate.Article.AuthorID])
		followingBonus := 0.0
		_, followed := profile.FollowingAuthorIDs[candidate.Article.AuthorID]
		if followed {
			followingBonus = cfg.FollowingBonus
		}
		authorScore := clampUnit(interactionAffinity + followingBonus)
		candidate.IsInNetwork = followed
		candidate.IsNovelAuthor = !candidate.IsInNetwork && interactionAffinity <= 0
		candidate.Breakdown = recommendationScoreBreakdown{
			PositiveSemantic: positiveSemantic, NegativeSemantic: negativeSemantic, NegativeConfidence: negativeConfidence,
			InteractionAffinity: interactionAffinity, FollowingBonusApplied: followingBonus,
			SemanticComponent:       cfg.SemanticWeight * semanticRaw,
			FreshnessComponent:      cfg.FreshnessWeight * freshness,
			PopularityComponent:     cfg.PopularityWeight * popularity,
			AuthorAffinityComponent: cfg.AuthorAffinityWeight * authorScore,
		}
		candidate.Breakdown.BaseScore = candidate.Breakdown.SemanticComponent +
			candidate.Breakdown.FreshnessComponent + candidate.Breakdown.PopularityComponent +
			candidate.Breakdown.AuthorAffinityComponent
		candidate.Breakdown.FinalScore = candidate.Breakdown.BaseScore
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return recommendationCandidateBaseBefore(candidates[i], candidates[j])
	})
	return candidates
}

func recommendationCandidateBaseBefore(left, right hydratedRecommendationCandidate) bool {
	if left.Breakdown.BaseScore != right.Breakdown.BaseScore {
		return left.Breakdown.BaseScore > right.Breakdown.BaseScore
	}
	leftTime := recommendationArticleTime(left.Article)
	rightTime := recommendationArticleTime(right.Article)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Article.ID > right.Article.ID
}

func recommendationArticleTime(article models.Article) time.Time {
	if article.PublishedAt != nil && !article.PublishedAt.IsZero() {
		return article.PublishedAt.UTC()
	}
	return article.CreatedAt.UTC()
}

func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// Compatibility seam for the existing unit tests; the V2 ranker uses the same
// publication-aware freshness and like+comment popularity components.
func scoreEmbeddingCandidate(candidate embeddingCandidate, article models.Article, now time.Time, cfg config.RecommendationConfig) float64 {
	semantic := 0.0
	if candidate.FromSemantic {
		semantic = clampRecommendationSimilarity(candidate.SemanticSimilarity)
	}
	ageDays := now.Sub(recommendationArticleTime(article)).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	freshness := math.Exp(-math.Ln2 * ageDays / cfg.FreshnessHalfLifeDays)
	popularity := math.Log1p(math.Max(0, float64(article.LikeCount))) +
		cfg.PopularityCommentFactor*math.Log1p(math.Max(0, float64(article.CommentCount)))
	return cfg.SemanticWeight*semantic + cfg.FreshnessWeight*freshness + cfg.PopularityWeight*popularity
}

func rankEmbeddingCandidates(profile userInterestProfile, candidates []embeddingCandidate, articles []models.Article, now time.Time, cfg config.RecommendationConfig, limit int) []recommendedArticleResponse {
	candidateByID := make(map[uint]embeddingCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.ArticleID] = candidate
	}
	result := make([]recommendedArticleResponse, 0, len(articles))
	for _, article := range articles {
		if _, interacted := profile.InteractedArticleIDs[article.ID]; interacted {
			continue
		}
		candidate := candidateByID[article.ID]
		result = append(result, recommendedArticleResponse{
			ID: article.ID, Title: article.Title, Content: article.Content, Preview: article.Preview,
			CoverImageURL: article.CoverImageURL, LikeCount: article.LikeCount, CommentCount: article.CommentCount,
			ViewCount: article.ViewCount, CreatedAt: article.CreatedAt, Author: publicAuthorFromUser(article.Author),
			Score: scoreEmbeddingCandidate(candidate, article, now, cfg),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		leftTime := result[i].CreatedAt
		rightTime := result[j].CreatedAt
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return result[i].ID > result[j].ID
	})
	if limit <= 0 || limit > maxRecommendationLimit {
		limit = defaultRecommendationLimit
	}
	if len(result) > limit {
		return result[:limit]
	}
	return result
}
