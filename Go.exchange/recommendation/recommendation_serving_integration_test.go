package recommendation

import (
	"context"
	"testing"
	"time"

	"Go.exchange/models"
	"github.com/google/uuid"
)

func TestRecommendationServiceAuthenticatedGormServingIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	viewer := newRecommendationProfileControllerIntegrationUser(t, db, "authenticated-serving-viewer")
	author := newRecommendationProfileControllerIntegrationUser(t, db, "authenticated-serving-author")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	cfg := testDefaultRecommendationConfig()
	version := recommendationProfileControllerIntegrationEmbeddingVersion

	profile := models.UserRecoProfile{
		UserID: viewer.ID, ProfileVersion: MaterializedProfileVersion,
		ProfileConfigHash: ProfileConfigHash(cfg, version), EmbeddingVersion: version,
		Dimensions: 0, ComputedAt: now.Add(-time.Minute), NextRebuildAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}

	const candidateCount = 30
	postIDs := make([]uint, 0, candidateCount)
	for index := 0; index < candidateCount; index++ {
		article := newRecommendationProfileControllerIntegrationPost(
			t, db, author.ID, "authenticated-serving-candidate", now.Add(-time.Duration(index+1)*time.Minute),
		)
		postIDs = append(postIDs, article.ID)
		addRecommendationProfileControllerServingEmbedding(t, db, article)
		if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Update("like_count", int64(candidateCount-index)).Error; err != nil {
			t.Fatal(err)
		}
	}

	candidates, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := NewGormTraceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	t.Cleanup(func() {
		db.Unscoped().Where("request_id = ?", requestID).Delete(&models.RecommendationRequest{})
		db.Unscoped().Where("request_id = ?", requestID).Delete(&models.RecommendationResultTrace{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	})
	dispatcher, err := NewAsyncTraceDispatcher(traces, NoopMetrics{}, cfg.Trace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Trace.ShutdownDrainTimeoutMS)*time.Millisecond)
		defer cancel()
		_ = dispatcher.Shutdown(cleanupCtx)
	})
	service, err := NewService(ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles},
		ServingVersions:  serviceTestVersionProvider{version: version},
		TraceEnqueuer:    dispatcher,
	}, ServiceConfig{Recommendation: cfg})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Serve(context.Background(), ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: viewer.ID}, Limit: 20, RequestID: requestID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Viewer != (Viewer{Kind: ViewerAuthenticated, UserID: viewer.ID}) || result.EmbeddingVersion != version {
		t.Fatalf("service viewer/version=%#v/%q", result.Viewer, result.EmbeddingVersion)
	}
	if result.StrategyID != RecommendationPersonalizedStrategyID || result.FreshCandidateSummary.RecentCount == 0 || len(result.Selected) == 0 {
		t.Fatalf("authenticated service result=%#v", result)
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Duration(cfg.Trace.ShutdownDrainTimeoutMS)*time.Millisecond)
	if err := dispatcher.Shutdown(drainCtx); err != nil {
		cancelDrain()
		t.Fatalf("drain serving trace dispatcher: %v", err)
	}
	cancelDrain()

	var persisted int64
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", requestID).Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("serving trace persisted %d request rows, want 1", persisted)
	}
}
