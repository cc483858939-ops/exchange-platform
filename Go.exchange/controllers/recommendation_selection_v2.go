package controllers

import (
	"math"
	"sort"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

type recommendationSelectionMode int

const (
	recommendationSelectionFresh recommendationSelectionMode = iota
	recommendationSelectionSoft
)

type selectedRecommendation struct {
	Candidate     embeddingCandidate
	Article       models.Article
	Embedding     []float32
	Breakdown     recommendationScoreBreakdown
	IsInNetwork   bool
	IsNovelAuthor bool
}

func balancedPositions(limit, target int) []int {
	if limit <= 0 || target <= 0 {
		return nil
	}
	if target >= limit {
		result := make([]int, limit)
		for index := range result {
			result[index] = index + 1
		}
		return result
	}
	result := make([]int, 0, target)
	used := make(map[int]struct{}, target)
	for k := 1; k <= target; k++ {
		position := int(math.Round(float64(k*limit) / float64(target)))
		if position < 1 {
			position = 1
		}
		if position > limit {
			position = limit
		}
		if _, exists := used[position]; exists {
			for distance := 1; distance <= limit; distance++ {
				left, right := position-distance, position+distance
				if left >= 1 {
					if _, exists := used[left]; !exists {
						position = left
						break
					}
				}
				if right <= limit {
					if _, exists := used[right]; !exists {
						position = right
						break
					}
				}
			}
		}
		used[position] = struct{}{}
		result = append(result, position)
	}
	sort.Ints(result)
	return result
}

func selectRecommendationCandidates(candidates []hydratedRecommendationCandidate, initial []selectedRecommendation, limit int, cfg config.RecommendationConfig, now time.Time, mode recommendationSelectionMode) []selectedRecommendation {
	if limit <= 0 {
		return nil
	}
	result := append([]selectedRecommendation(nil), initial...)
	selectedIDs := make(map[uint]struct{}, len(result))
	for _, item := range result {
		selectedIDs[item.Article.ID] = struct{}{}
	}
	outPositions := make(map[int]struct{})
	for _, position := range balancedPositions(limit, int(math.Round(float64(limit)*cfg.OutOfNetworkMinRatio))) {
		outPositions[position] = struct{}{}
	}
	novelPositions := make(map[int]struct{})
	for _, position := range balancedPositions(limit, int(math.Round(float64(limit)*cfg.NovelAuthorMinRatio))) {
		novelPositions[position] = struct{}{}
	}

	for len(result) < limit {
		position := len(result) + 1
		onlyFresh := mode == recommendationSelectionFresh
		onlySoft := mode == recommendationSelectionSoft
		available := func(item hydratedRecommendationCandidate) bool {
			if _, exists := selectedIDs[item.Article.ID]; exists {
				return false
			}
			if onlyFresh && item.Candidate.WasSoftServed {
				return false
			}
			if onlySoft && !item.Candidate.WasSoftServed {
				return false
			}
			return true
		}
		preferNovel := func(item hydratedRecommendationCandidate) bool {
			if _, ok := novelPositions[position]; ok {
				return item.IsNovelAuthor
			}
			if _, ok := outPositions[position]; ok {
				return !item.IsInNetwork
			}
			return true
		}
		secondary := func(item hydratedRecommendationCandidate) bool {
			if _, ok := novelPositions[position]; ok {
				return !item.IsInNetwork
			}
			return true
		}
		index, chosen, ok := chooseRecommendationCandidate(candidates, result, available, preferNovel, cfg, mode, now)
		if !ok {
			index, chosen, ok = chooseRecommendationCandidate(candidates, result, available, secondary, cfg, mode, now)
		}
		if !ok {
			index, chosen, ok = chooseRecommendationCandidate(candidates, result, available, func(hydratedRecommendationCandidate) bool { return true }, cfg, mode, now)
		}
		if !ok {
			break
		}
		selectedIDs[chosen.Article.ID] = struct{}{}
		chosen.Breakdown.FinalScore = chosen.Breakdown.BaseScore - chosen.Breakdown.DiversityPenalty
		result = append(result, selectedRecommendation{
			Candidate: chosen.Candidate, Article: chosen.Article, Embedding: chosen.Embedding,
			Breakdown: chosen.Breakdown, IsInNetwork: chosen.IsInNetwork, IsNovelAuthor: chosen.IsNovelAuthor,
		})
		_ = index
	}
	return result
}

func chooseRecommendationCandidate(candidates []hydratedRecommendationCandidate, selected []selectedRecommendation, available func(hydratedRecommendationCandidate) bool, preferred func(hydratedRecommendationCandidate) bool, cfg config.RecommendationConfig, mode recommendationSelectionMode, now time.Time) (int, hydratedRecommendationCandidate, bool) {
	bestIndex := -1
	var best hydratedRecommendationCandidate
	found := false
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = recommendationDiversityPenalty(evaluated, selected, cfg)
		if !recommendationAuthorWindowAllows(evaluated, selected, cfg) {
			continue
		}
		if !found || recommendationSelectionBefore(evaluated, best, mode) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	if found {
		return bestIndex, best, true
	}
	// A shortage may relax only the author rule; public, NI, interacted, self,
	// and hard-served eligibility was already enforced by the source query.
	for index, candidate := range candidates {
		if !available(candidate) || !preferred(candidate) {
			continue
		}
		evaluated := candidate
		evaluated.Breakdown.DiversityPenalty = recommendationDiversityPenalty(evaluated, selected, cfg)
		if !found || recommendationSelectionBefore(evaluated, best, mode) {
			found, bestIndex, best = true, index, evaluated
		}
	}
	return bestIndex, best, found
}

func recommendationAuthorWindowAllows(candidate hydratedRecommendationCandidate, selected []selectedRecommendation, cfg config.RecommendationConfig) bool {
	if !cfg.Diversity.Enabled || cfg.Diversity.AuthorWindowSize <= 0 || cfg.Diversity.MaxSameAuthorInWindow <= 0 {
		return true
	}
	start := len(selected) - cfg.Diversity.AuthorWindowSize
	if start < 0 {
		start = 0
	}
	count := 0
	for index := start; index < len(selected); index++ {
		if selected[index].Article.AuthorID == candidate.Article.AuthorID {
			count++
		}
	}
	return count < cfg.Diversity.MaxSameAuthorInWindow
}

func recommendationDiversityPenalty(candidate hydratedRecommendationCandidate, selected []selectedRecommendation, cfg config.RecommendationConfig) float64 {
	if !cfg.Diversity.Enabled || !validEmbeddingVector(candidate.Embedding) || len(selected) == 0 {
		return 0
	}
	maxSimilarity := -1.0
	for _, item := range selected {
		if !validEmbeddingVector(item.Embedding) || len(item.Embedding) != len(candidate.Embedding) {
			continue
		}
		maxSimilarity = math.Max(maxSimilarity, cosineSimilarity(candidate.Embedding, item.Embedding))
	}
	if maxSimilarity >= cfg.Diversity.SemanticDuplicateThreshold {
		return cfg.Diversity.SemanticDuplicatePenalty
	}
	return 0
}

func recommendationSelectionBefore(left, right hydratedRecommendationCandidate, mode recommendationSelectionMode) bool {
	if mode == recommendationSelectionSoft && !left.Candidate.LastServedAt.Equal(right.Candidate.LastServedAt) {
		if left.Candidate.LastServedAt.IsZero() {
			return false
		}
		if right.Candidate.LastServedAt.IsZero() {
			return true
		}
		return left.Candidate.LastServedAt.Before(right.Candidate.LastServedAt)
	}
	leftScore := left.Breakdown.BaseScore - left.Breakdown.DiversityPenalty
	rightScore := right.Breakdown.BaseScore - right.Breakdown.DiversityPenalty
	if leftScore != rightScore {
		return leftScore > rightScore
	}
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

func selectedRecommendationResponses(selected []selectedRecommendation) []recommendedArticleResponse {
	result := make([]recommendedArticleResponse, 0, len(selected))
	for _, item := range selected {
		result = append(result, recommendedArticleResponse{
			ID: item.Article.ID, Title: item.Article.Title, Content: item.Article.Content, Preview: item.Article.Preview,
			CoverImageURL: item.Article.CoverImageURL, LikeCount: item.Article.LikeCount, CommentCount: item.Article.CommentCount,
			ViewCount: item.Article.ViewCount, CreatedAt: item.Article.CreatedAt, Author: publicAuthorFromUser(item.Article.Author),
			Score: item.Breakdown.FinalScore,
		})
	}
	return result
}
