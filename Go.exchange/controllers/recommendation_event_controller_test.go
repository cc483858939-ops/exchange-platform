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

func signTestRecommendationToken(t *testing.T, userID, articleID uint, now time.Time, estimated int64, policy string) string {
	t.Helper()
	token, err := signRecommendationTrackingClaims(recommendationTrackingClaims{
		UserID: userID, RequestID: uuid.NewString(), ArticleID: articleID, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: estimated, ReadPolicyVersion: policy,
	}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestRecordRecommendationEventsReturnsPartialResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := "0123456789abcdef0123456789abcdef"
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

	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
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
	if response.Accepted != 1 || response.Rejected != 1 || response.Duplicates != 0 ||
		response.Results[1].Reason != "invalid_tracking_token" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestValidateRecommendationTelemetryEventUsesSignedReadContext(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	foreground := int64(2000)
	progress := 100
	exitType := "route_leave"
	event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeReadEnd,
		TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
		ForegroundTimeMS: &foreground, ScrollProgressPercent: &progress, ExitType: &exitType,
	}, now, key)
	if reason != "" {
		t.Fatalf("reason=%q", reason)
	}
	if event.EstimatedReadTimeMS == nil || *event.EstimatedReadTimeMS != 3000 ||
		event.ReadPolicyVersion == nil || *event.ReadPolicyVersion != recommendationReadPolicyVersion ||
		event.ReadOutcome == nil || *event.ReadOutcome != recommendationReadOutcomeNeutral {
		t.Fatalf("unexpected persisted read context: %#v", event)
	}
}

func TestValidateRecommendationTelemetryEventRejectsUserMismatch(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	token := signTestRecommendationToken(t, 8, 11, now, 3000, recommendationReadPolicyVersion)
	_, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick,
		TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}, now, key)
	if reason != "user_mismatch" {
		t.Fatalf("reason=%q", reason)
	}
}

func TestValidateRecommendationTelemetryEventFeedDwellPayload(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	valid := int64(400)
	zero := int64(0)
	negative := int64(-1)
	tooLong := recommendationFeedDwellMaxVisibleTimeMS + 1
	foreground := int64(1000)
	progress := 20
	exitType := "route_leave"

	tests := []struct {
		name       string
		eventType  string
		feed       *int64
		foreground *int64
		progress   *int
		exitType   *string
		wantReason string
	}{
		{name: "valid", eventType: models.RecommendationEventTypeFeedDwell, feed: &valid},
		{name: "missing payload", eventType: models.RecommendationEventTypeFeedDwell, wantReason: "missing_feed_payload"},
		{name: "zero", eventType: models.RecommendationEventTypeFeedDwell, feed: &zero, wantReason: "invalid_feed_visible_time_ms"},
		{name: "negative", eventType: models.RecommendationEventTypeFeedDwell, feed: &negative, wantReason: "invalid_feed_visible_time_ms"},
		{name: "over six hours", eventType: models.RecommendationEventTypeFeedDwell, feed: &tooLong, wantReason: "invalid_feed_visible_time_ms"},
		{name: "click payload", eventType: models.RecommendationEventTypeClick, feed: &valid, wantReason: "unexpected_feed_payload"},
		{name: "read_end payload", eventType: models.RecommendationEventTypeReadEnd, feed: &valid, wantReason: "unexpected_feed_payload"},
		{
			name:      "feed with detail payload",
			eventType: models.RecommendationEventTypeFeedDwell,
			feed:      &valid, foreground: &foreground, progress: &progress, exitType: &exitType,
			wantReason: "unexpected_feed_payload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
			event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
				EventID: uuid.NewString(), EventType: tc.eventType,
				TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
				ForegroundTimeMS: tc.foreground, ScrollProgressPercent: tc.progress,
				ExitType: tc.exitType, FeedVisibleTimeMS: tc.feed,
			}, now, key)
			if reason != tc.wantReason {
				t.Fatalf("reason=%q want=%q event=%#v", reason, tc.wantReason, event)
			}
			if tc.wantReason == "" {
				if event.EventType != models.RecommendationEventTypeFeedDwell ||
					event.FeedVisibleTimeMS == nil || *event.FeedVisibleTimeMS != valid ||
					event.ForegroundTimeMS != nil || event.ReadOutcome != nil {
					t.Fatalf("unexpected feed event=%#v", event)
				}
			}
		})
	}

	if got := recommendationEventTypeMetricLabel(models.RecommendationEventTypeFeedDwell); got != models.RecommendationEventTypeFeedDwell {
		t.Fatalf("metric label=%q", got)
	}
}
