package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRecordRecommendationEventsReturnsPartialResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := "0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", key)

	originalAllow := allowRecommendationTelemetryEvents
	originalPersist := persistRecommendationEventBatch
	originalNow := recommendationTelemetryNow
	t.Cleanup(func() {
		allowRecommendationTelemetryEvents = originalAllow
		persistRecommendationEventBatch = originalPersist
		recommendationTelemetryNow = originalNow
	})
	allowRecommendationTelemetryEvents = func(uint, int) (bool, error) { return true, nil }
	recommendationTelemetryNow = func() time.Time { return now }
	persistRecommendationEventBatch = func(userID uint, events []models.RecommendationEvent) ([]recommendationPersistenceResult, error) {
		if userID != 7 || len(events) != 1 || events[0].ArticleID != 11 {
			t.Fatalf("unexpected persisted events: user=%d events=%#v", userID, events)
		}
		return []recommendationPersistenceResult{{Status: "accepted"}}, nil
	}

	token, err := signRecommendationTrackingClaims(recommendationTrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), ArticleID: 11, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	request := recommendationEventBatchRequest{Events: []recommendationEventInput{
		{EventID: uuid.NewString(), EventType: models.RecommendationEventTypeImpression, TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano)},
		{EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick, TrackingToken: token + "tampered", OccurredAt: now.Format(time.RFC3339Nano)},
	}}
	body, _ := json.Marshal(request)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/recommendation-events", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	RecordRecommendationEvents(ctx)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response recommendationEventBatchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Accepted != 1 || response.Rejected != 1 || response.Duplicates != 0 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Results[1].Reason != "invalid_tracking_token" {
		t.Fatalf("unexpected rejected result: %#v", response.Results[1])
	}
}

func TestValidateRecommendationTelemetryEventRejectsUserMismatch(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	token, err := signRecommendationTrackingClaims(recommendationTrackingClaims{
		UserID: 8, RequestID: uuid.NewString(), ArticleID: 11, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	_, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick,
		TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}, now, key)
	if reason != "user_mismatch" {
		t.Fatalf("reason=%q", reason)
	}
}
