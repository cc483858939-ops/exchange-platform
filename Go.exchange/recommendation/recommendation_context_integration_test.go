package recommendation

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/models"
	"github.com/google/uuid"
)

func TestRecommendationRepositoriesHonorCanceledContextIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Now().UTC()
	cfg := testDefaultRecommendationConfig()

	candidateRepository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateRepository.LoadPublicRecentCandidates(canceled, PublicCandidateQuery{Now: now, Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("candidate repository error=%v, want context.Canceled", err)
	}

	profileRepository, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profileRepository.Load(canceled, ProfileLoadQuery{
		UserID: 1, EmbeddingVersion: "post_embedding_v1",
		ExpectedProfileVersion:    MaterializedProfileVersion,
		ExpectedProfileConfigHash: ProfileConfigHash(cfg, "post_embedding_v1"),
		Now:                       now, NegativeConfidenceHalfLifeDays: cfg.SignalHalfLifeDays,
		NegativeConfidenceSaturation: cfg.NegativeConfidenceSaturationScale,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("profile repository error=%v, want context.Canceled", err)
	}

	traceRepository, err := NewGormTraceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	canceledRequestID := uuid.NewString()
	canceledRequest := models.RecommendationRequest{
		RequestID: canceledRequestID, Scene: RecommendationScene, StrategyID: RecommendationColdStartStrategyID,
		RankerVersion: RankerVersion, RankerConfigHash: "canceled-context-test", RequestedLimit: 1,
		PersonalizationMode: "cold_start", CreatedAt: now,
	}
	if err := traceRepository.PersistServing(canceled, canceledRequest, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("trace repository error=%v, want context.Canceled", err)
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
	if err := traceRepository.PersistServing(context.Background(), liveRequest, nil); err != nil {
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
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("database became unusable after canceled scoped reads: %v", err)
	}
}
