package controllers

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
	"Go.exchange/recommendation"
)

func TestAuthenticatedRecommendationServingDoesNotUseGuestDiversificationIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	viewer := newRecommendationProfileControllerIntegrationUser(t, db, "authenticated-serving-viewer")
	author := newRecommendationProfileControllerIntegrationUser(t, db, "authenticated-serving-author")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()

	profile := models.UserRecoProfile{
		UserID:            viewer.ID,
		ProfileVersion:    recommendation.MaterializedProfileVersion,
		ProfileConfigHash: recommendation.ProfileConfigHash(cfg, config.ServingEmbeddingVersion()),
		EmbeddingVersion:  config.ServingEmbeddingVersion(),
		Dimensions:        0,
		ComputedAt:        now.Add(-time.Minute),
		NextRebuildAt:     now.Add(time.Hour),
		UpdatedAt:         now,
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}

	const candidateCount = 30
	for index := 0; index < candidateCount; index++ {
		article := newRecommendationProfileControllerIntegrationPost(
			t, db, author.ID, "authenticated-serving-candidate", now.Add(-time.Duration(index+1)*time.Minute),
		)
		if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Updates(map[string]interface{}{
			"like_count": int64(candidateCount - index),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	originalDiversifier := diversifyPublicRecommendationCandidatesForServing
	calls := 0
	diversifyPublicRecommendationCandidatesForServing = func(ranked []hydratedRecommendationCandidate, limit int, requestID string) []hydratedRecommendationCandidate {
		calls++
		return originalDiversifier(ranked, limit, requestID)
	}
	t.Cleanup(func() {
		diversifyPublicRecommendationCandidatesForServing = originalDiversifier
	})

	outcome, err := serveRecommendationCandidatePath(
		context.Background(),
		db,
		viewer.ID,
		20,
		cfg,
		now,
		"authenticated-boundary",
		recommendationLanguageContext{},
		map[uint]servedPost{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.RecallSets) == 0 {
		t.Fatal("authenticated serving did not produce a recall set")
	}
	if len(outcome.Selected) == 0 {
		t.Fatal("authenticated serving selected no recommendations")
	}
	if calls != 0 {
		t.Fatalf("authenticated serving invoked guest diversification: calls=%d", calls)
	}
}
