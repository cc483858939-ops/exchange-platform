package recommendation

import (
	"testing"
	"time"

	"Go.exchange/models"
)

func TestSameRecommendationTraceIgnoresDynamicProfileAge(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	stored := models.RecommendationRequest{RequestID: "request", UserID: 42, Scene: RecommendationScene, ProfileAgeMS: 1000, CreatedAt: now}
	retry := stored
	retry.ProfileAgeMS = 1250
	if !sameRecommendationTrace(stored, retry, nil, nil) {
		t.Fatal("retry with advancing diagnostic profile age was treated as a trace conflict")
	}
	retry.CreatedAt = now.Add(time.Second)
	if sameRecommendationTrace(stored, retry, nil, nil) {
		t.Fatal("retry with a changed immutable creation time was accepted")
	}
}
