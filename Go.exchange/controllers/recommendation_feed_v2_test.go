package controllers

import (
	"math"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestCanonicalV2PreservesLikeReplyAndNIPrecedence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	readOutcome := recommendationReadOutcomeQualified
	outcomes := canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{
			{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(time.Minute)}},
		},
		[]recommendationFeedbackSignal{
			{Event: recommendationFeedbackEvent{PostID: 7, EventID: "read", EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &readOutcome}},
		},
		map[uint]recommendationReactionState{7: {Liked: true, StateChangedAt: now.Add(2 * time.Minute)}},
	)
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 2 {
		t.Fatalf("outcomes=%#v", outcomes)
	}
	if outcomes[0].PositiveSignals[0].SignalType != "like" || outcomes[0].PositiveSignals[1].SignalType != "reply" {
		t.Fatalf("positive signals=%#v", outcomes[0].PositiveSignals)
	}

	ni := recommendationFeedbackSignal{Event: recommendationFeedbackEvent{
		PostID: 7, EventID: "ni", EventType: recommendationFeedbackEventTypeNotInterested, OccurredAt: now.Add(3 * time.Minute),
	}}
	outcomes = canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(2 * time.Minute)}}},
		[]recommendationFeedbackSignal{ni},
		map[uint]recommendationReactionState{7: {Liked: true, StateChangedAt: now.Add(3 * time.Minute)}},
	)
	if len(outcomes) != 1 || outcomes[0].NegativeSignal == nil || outcomes[0].NegativeSignal.SignalType != "not_interested" || len(outcomes[0].PositiveSignals) != 0 {
		t.Fatalf("equal-time NI must win: %#v", outcomes)
	}

	outcomes = canonicalizeRecommendationOutcomes(
		[]postBehaviorSignal{{Behavior: models.PostBehavior{PostID: 7, Action: PostBehaviorActionReply, LastSeenAt: now.Add(4 * time.Minute)}}},
		[]recommendationFeedbackSignal{ni},
		nil,
	)
	if len(outcomes) != 1 || len(outcomes[0].PositiveSignals) != 1 || outcomes[0].PositiveSignals[0].SignalType != "reply" || outcomes[0].NegativeSignal != nil {
		t.Fatalf("later reply must restore positive precedence: %#v", outcomes)
	}
}

func TestRecommendationProfileCapsPositivePostAndSeparatesNegativeVector(t *testing.T) {
	original := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		return map[uint][]float32{1: {1, 0}, 2: {0, 1}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = original })

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()
	cfg.PositivePostWeightCap = cfg.BehaviorWeights.Reply
	quick := recommendationReadOutcomeQuickBounce
	profile, err := buildEmbeddingInterestProfile(
		[]postBehaviorSignal{
			{Behavior: models.PostBehavior{Model: gorm.Model{ID: 1}, PostID: 1, Action: PostBehaviorActionReply, LastSeenAt: now}},
			{Behavior: models.PostBehavior{Model: gorm.Model{ID: 2}, PostID: 2, Action: PostBehaviorActionView, LastSeenAt: now}},
		},
		[]recommendationFeedbackSignal{
			{Event: recommendationFeedbackEvent{PostID: 2, EventID: "bounce", EventType: recommendationFeedbackEventTypeReadEnd, OccurredAt: now, ReadOutcome: &quick}},
		},
		map[uint]recommendationReactionState{1: {Liked: true, StateChangedAt: now}},
		now, cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PositiveContributions[1] != cfg.PositivePostWeightCap {
		t.Fatalf("like+reply contribution=%v want cap=%v", profile.PositiveContributions[1], cfg.PositivePostWeightCap)
	}
	if profile.PositiveSignalCount != 1 || profile.NegativeSignalCount != 1 {
		t.Fatalf("profile counts=%#v", profile)
	}
	if len(profile.PositiveVector) == 0 || len(profile.NegativeVector) == 0 || profile.NegativeConfidence <= 0 || profile.NegativeConfidence >= 1 {
		t.Fatalf("profile vectors/confidence=%#v", profile)
	}
	if math.Abs(float64(profile.PositiveVector[0])) < 0.99 || math.Abs(float64(profile.NegativeVector[1])) < 0.99 {
		t.Fatalf("profile vector directions positive=%v negative=%v", profile.PositiveVector, profile.NegativeVector)
	}
}

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
		{Candidate: embeddingCandidate{PostID: 1, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 1}, AuthorID: 1}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)}, Embedding: []float32{0, 1}},
		{Candidate: embeddingCandidate{PostID: 2, FromSemantic: true, PositiveSemanticSimilarity: .5}, Post: models.Post{Model: gorm.Model{ID: 2}, AuthorID: 2}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)}, Embedding: []float32{1, 0}},
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
		Post:      models.Post{Model: gorm.Model{ID: 1}, AuthorID: 1}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now)},
		Breakdown: recommendationScoreBreakdown{BaseScore: 2},
	}}
	soft := []hydratedRecommendationCandidate{{
		Candidate: embeddingCandidate{PostID: 2, FromTrending: true, WasSoftServed: true, LastServedAt: now.Add(-time.Hour)},
		Post:      models.Post{Model: gorm.Model{ID: 2}, AuthorID: 2}, PostArticle: &models.PostArticle{PublishedAt: ptrTime(now.Add(-time.Minute))},
		Breakdown: recommendationScoreBreakdown{BaseScore: 1},
	}}
	selected := selectRecommendationCandidates(fresh, nil, 2, cfg, now, recommendationSelectionFresh)
	selected = selectRecommendationCandidates(soft, selected, 2, cfg, now, recommendationSelectionSoft)
	if len(selected) != 2 || selected[0].Post.ID != 1 || selected[1].Post.ID != 2 {
		t.Fatalf("selected=%#v", selected)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
