package recommendation

import (
	"math"
	"sort"
	"time"

	"Go.exchange/models"
)

func RankCandidates(profile ProfileFeatures, candidates []RankedCandidate, now time.Time, cfg RankingConfig, languageContexts ...LanguageContext) []RankedCandidate {
	languageContext := LanguageContext{}
	if len(languageContexts) > 0 {
		languageContext = languageContexts[0]
	}
	for index := range candidates {
		candidate := &candidates[index]
		positiveSemantic := 0.0
		if validComparableEmbedding(candidate.Embedding, profile.PositiveVector) {
			positiveSemantic = ClampUnit(CosineSimilarity(candidate.Embedding, profile.PositiveVector))
		} else if candidate.Candidate.FromSemantic && ValidEmbeddingVector(profile.PositiveVector) {
			positiveSemantic = clampSemanticSimilarity(candidate.Candidate.PositiveSemanticSimilarity)
		}
		negativeSemantic := CosineSimilarity(candidate.Embedding, profile.NegativeVector)
		if negativeSemantic < 0 {
			negativeSemantic = 0
		}
		negativeConfidence := profile.NegativeConfidence
		semanticRaw := positiveSemantic - cfg.NegativeSemanticWeight*negativeConfidence*negativeSemantic
		trendingRaw := TrendingRaw(candidate.Post, now, cfg.Trending)
		interactionAffinity := ClampUnit(profile.AuthorAffinity[candidate.Post.AuthorID])
		followingBonus := 0.0
		_, followed := profile.FollowingAuthorIDs[candidate.Post.AuthorID]
		if followed {
			followingBonus = cfg.FollowingBonus
		}
		authorScore := ClampUnit(interactionAffinity + followingBonus)
		languageAffinity := 0.0
		languageComponent := 0.0
		if cfg.Language.Enabled {
			languageAffinity, languageComponent = ScoreLanguage(candidate.Post.Language, languageContext.Combined, cfg.Language)
		}
		candidate.IsInNetwork = followed
		candidate.IsNovelAuthor = !candidate.IsInNetwork && interactionAffinity <= 0
		candidate.Breakdown = ScoreBreakdown{
			PositiveSemantic: positiveSemantic, NegativeSemantic: negativeSemantic, NegativeConfidence: negativeConfidence,
			InteractionAffinity: interactionAffinity, FollowingBonusApplied: followingBonus,
			SemanticComponent:       cfg.SemanticWeight * semanticRaw,
			TrendingComponent:       cfg.TrendingWeight * trendingRaw,
			AuthorAffinityComponent: cfg.AuthorAffinityWeight * authorScore,
			LanguageAffinity:        languageAffinity,
			LanguageComponent:       languageComponent,
		}
		candidate.Breakdown.BaseScore = candidate.Breakdown.SemanticComponent +
			candidate.Breakdown.TrendingComponent +
			candidate.Breakdown.AuthorAffinityComponent +
			candidate.Breakdown.LanguageComponent
		candidate.Breakdown.FinalScore = candidate.Breakdown.BaseScore
		candidate.ExplorationSemantic = explorationSemantic(*candidate, profile, cfg)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return rankedCandidateBefore(candidates[i], candidates[j])
	})
	return candidates
}

func explorationSemantic(candidate RankedCandidate, profile ProfileFeatures, cfg RankingConfig) float64 {
	positive := 0.0
	if validComparableEmbedding(candidate.Embedding, profile.PositiveVector) {
		positive = ClampUnit(CosineSimilarity(candidate.Embedding, profile.PositiveVector))
	}
	negative := 0.0
	if validComparableEmbedding(candidate.Embedding, profile.NegativeVector) {
		negative = ClampUnit(CosineSimilarity(candidate.Embedding, profile.NegativeVector))
	}
	confidence := ClampUnit(profile.NegativeConfidence)
	raw := positive - cfg.NegativeSemanticWeight*confidence*negative
	return ClampUnit(raw)
}

func validComparableEmbedding(left, right []float32) bool {
	return ValidEmbeddingVector(left) && ValidEmbeddingVector(right) && len(left) == len(right)
}

func TrendingRaw(post models.Post, now time.Time, cfg TrendingConfig) float64 {
	postTime := recommendationPostTime(post)
	return trendingRawAt(post, postTime, now, cfg)
}

func trendingRawAt(post models.Post, postTime, now time.Time, cfg TrendingConfig) float64 {
	ageHours := now.Sub(postTime).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	if cfg.MaxAgeDays <= 0 || ageHours > float64(cfg.MaxAgeDays)*24 || cfg.HalfLifeHours <= 0 {
		return 0
	}
	engagement := math.Log1p(math.Max(0, float64(post.LikeCount))) +
		cfg.ReplyFactor*math.Log1p(math.Max(0, float64(post.ReplyCount)))
	if engagement <= 0 {
		return 0
	}
	decay := math.Exp(-math.Ln2 * ageHours / cfg.HalfLifeHours)
	return engagement * decay
}

func rankedCandidateBefore(left, right RankedCandidate) bool {
	if left.Breakdown.BaseScore != right.Breakdown.BaseScore {
		return left.Breakdown.BaseScore > right.Breakdown.BaseScore
	}
	leftTime := recommendationPostTime(left.Post)
	rightTime := recommendationPostTime(right.Post)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.Post.ID > right.Post.ID
}

func recommendationPostTime(post models.Post) time.Time {
	return post.CreatedAt.UTC()
}

func ClampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampSemanticSimilarity(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}

func ClampSemanticSimilarity(value float64) float64 {
	return clampSemanticSimilarity(value)
}

func NonNegativeScore(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
