package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
)

func TestRecommendationOnlineDBOperationsHonorCanceledContextIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	scopedDB := db.WithContext(canceled)
	now := time.Now().UTC()
	cfg := normalizedRecommendationConfig()

	if _, err := loadMaterializedUserInterestProfile(scopedDB, 1, "post_embedding_v1", now, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("materialized profile error=%v, want context.Canceled", err)
	}
	if _, err := loadPublicRecommendationCandidateSet(scopedDB, "post_embedding_v1", now, cfg, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("public candidate error=%v, want context.Canceled", err)
	}
	if err := global.Db.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("global database became unusable after canceled scoped reads: %v", err)
	}

	canceledRequestID := uuid.NewString()
	canceledRequest := models.RecommendationRequest{
		RequestID: canceledRequestID, Scene: recommendationScene, StrategyID: recommendationColdStartStrategyID,
		RankerVersion: recommendationRankerVersion, RankerConfigHash: "canceled-context-test", RequestedLimit: 1,
		PersonalizationMode: "cold_start", CreatedAt: now,
	}
	traceRepository, err := recommendation.NewGormTraceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistRecommendationServingTraceViaRepository(canceled, traceRepository, canceledRequest, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("trace persistence error=%v, want context.Canceled", err)
	}
	var persisted int64
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", canceledRequestID).Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatalf("canceled trace persisted %d request rows", persisted)
	}

	liveRequestID := uuid.NewString()
	liveRequest := canceledRequest
	liveRequest.RequestID = liveRequestID
	if err := persistRecommendationServingTraceViaRepository(context.Background(), traceRepository, liveRequest, nil); err != nil {
		t.Fatalf("live trace persistence failed: %v", err)
	}
	if err := db.Model(&models.RecommendationRequest{}).Where("request_id = ?", liveRequestID).Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("live trace persisted %d request rows, want 1", persisted)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("request_id IN ?", []string{canceledRequestID, liveRequestID}).Delete(&models.RecommendationRequest{})
	})
}
