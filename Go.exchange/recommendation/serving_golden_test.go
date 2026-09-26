package recommendation

import (
	"testing"
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
)

func TestBalancedPositionsAreDeterministicAndBounded(t *testing.T) {
	if got := balancedPositions(5, 0); got != nil {
		t.Fatalf("target zero positions=%v", got)
	}
	if got := balancedPositions(5, 5); len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Fatalf("all positions=%v", got)
	}
	first := balancedPositions(20, 6)
	second := balancedPositions(20, 6)
	if len(first) != 6 || len(second) != 6 {
		t.Fatalf("positions first=%v second=%v", first, second)
	}
	for index := range first {
		if first[index] != second[index] || first[index] < 1 || first[index] > 20 {
			t.Fatalf("nondeterministic positions first=%v second=%v", first, second)
		}
	}
}

func TestRecommendationRankerPenalizesNegativeSimilarityWithConfidence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()
	profile := userInterestProfile{NegativeVector: []float32{1, 0}, NegativeConfidence: 1, AuthorAffinity: map[uint]float64{}, FollowingAuthorIDs: map[uint]struct{}{}}
	candidates := []hydratedRecommendationCandidate{
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 1}, Embedding: []float32{0, 1}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 2, CreatedAt: now}, AuthorID: 2}, Embedding: []float32{1, 0}},
	}
	ranked := rankRecommendationCandidates(profile, candidates, now, cfg)
	if len(ranked) != 2 || ranked[0].Post.ID != 1 || ranked[0].Breakdown.NegativeSemantic != 0 || ranked[1].Breakdown.NegativeSemantic < .99 {
		t.Fatalf("ranked=%#v", ranked)
	}
}

func TestRecommendationSelectionUsesFreshThenSoftWithoutDuplicate(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	fresh := []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 1, FromRecent: true},
		Post:      models.Post{Model: gorm.Model{ID: 1, CreatedAt: now}, AuthorID: 1},
		Breakdown: recommendationScoreBreakdown{BaseScore: 2},
	}}
	soft := []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 2, FromTrending: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)},
		Post:      models.Post{Model: gorm.Model{ID: 2, CreatedAt: now.Add(-time.Minute)}, AuthorID: 2},
		Breakdown: recommendationScoreBreakdown{BaseScore: 1},
	}}
	selected := selectRecommendationCandidates(fresh, nil, 2, cfg, now, recommendationSelectionFresh)
	selected = selectRecommendationCandidates(soft, selected, 2, cfg, now, recommendationSelectionSoft)
	if len(selected) != 2 || selected[0].Post.ID != 1 || selected[1].Post.ID != 2 {
		t.Fatalf("selected=%#v", selected)
	}
}
