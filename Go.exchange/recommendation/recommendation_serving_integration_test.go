package recommendation

import (
	"context"
	"errors"
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
	shutdownTimeout := time.Duration(cfg.Trace.ShutdownDrainTimeoutMS) * time.Millisecond
	if shutdownTimeout <= 0 {
		t.Fatalf("trace dispatcher shutdown timeout must be positive, got %dms", cfg.Trace.ShutdownDrainTimeoutMS)
	}
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
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

	serveRequest := ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: viewer.ID}, Limit: 20, RequestID: requestID, Now: now,
	}
	result, err := service.Serve(context.Background(), serveRequest)
	if err != nil {
		t.Fatal(err)
	}
	finalizeServiceTestResult(t, service, result)
	if result.Viewer != (Viewer{Kind: ViewerAuthenticated, UserID: viewer.ID}) || result.EmbeddingVersion != version {
		t.Fatalf("service viewer/version=%#v/%q", result.Viewer, result.EmbeddingVersion)
	}
	if result.StrategyID != RecommendationPersonalizedStrategyID || result.FreshCandidateSummary.RecentCount == 0 || len(result.Selected) == 0 {
		t.Fatalf("authenticated service result=%#v", result)
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
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

	traceRequest := buildRecommendationRequest(serveRequest, result, result.traceGenerationDuration, cfg)
	traceResults := buildResultTraces(traceRequest, result.Selected, result.Now, cfg)
	if err := traces.PersistServing(context.Background(), traceRequest, traceResults); err != nil {
		t.Fatalf("persist same serving trace twice: %v", err)
	}
	var resultRows int64
	if err := db.Model(&models.RecommendationResultTrace{}).Where("request_id = ?", requestID).Count(&resultRows).Error; err != nil {
		t.Fatal(err)
	}
	if resultRows != int64(len(traceResults)) {
		t.Fatalf("idempotent trace result rows=%d want %d", resultRows, len(traceResults))
	}
	conflictingResults := append([]models.RecommendationResultTrace(nil), traceResults...)
	conflictingResults[0].PostID++
	if err := traces.PersistServing(context.Background(), traceRequest, conflictingResults); !errors.Is(err, ErrTraceConflict) {
		t.Fatalf("same request with different trace results error=%v want ErrTraceConflict", err)
	}
}
