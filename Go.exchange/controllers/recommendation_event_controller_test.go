package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type recommendationTestPublisher struct {
	calls  int
	events []eventing.Envelope
	err    error
}

func (p *recommendationTestPublisher) PublishBatch(_ context.Context, events []eventing.Envelope) error {
	p.calls++
	p.events = append([]eventing.Envelope(nil), events...)
	return p.err
}

func signTestRecommendationToken(t *testing.T, userID, articleID uint, now time.Time, estimated int64, policy string) string {
	t.Helper()
	token, err := signRecommendationTrackingClaims(recommendationTrackingClaims{
		UserID: userID, RequestID: uuid.NewString(), ArticleID: articleID, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: estimated, ReadPolicyVersion: policy,
		SelectionMode: string(recommendationResultSelectionRanked),
	}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func recommendationTestContext(t *testing.T, request recommendationEventBatchRequest, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", userID)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/recommendation-events", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, recorder
}

func TestRecommendationEventsHandlerPublishesOnlyValidEventsWithoutDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := "0123456789abcdef0123456789abcdef"
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", key)
	originalAllow, originalNow, originalDB := allowRecommendationTelemetryEvents, recommendationTelemetryNow, global.Db
	t.Cleanup(func() {
		allowRecommendationTelemetryEvents = originalAllow
		recommendationTelemetryNow = originalNow
		global.Db = originalDB
	})
	allowRecommendationTelemetryEvents = func(uint, int) (bool, error) { return true, nil }
	recommendationTelemetryNow = func() time.Time { return now }
	global.Db = nil

	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	validID := uuid.NewString()
	request := recommendationEventBatchRequest{Events: []recommendationEventInput{
		{EventID: validID, EventType: models.RecommendationEventTypeImpression, TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano)},
		{EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick, TrackingToken: token + "tampered", OccurredAt: now.Format(time.RFC3339Nano)},
	}}
	publisher := &recommendationTestPublisher{}
	ctx, recorder := recommendationTestContext(t, request, 7)
	NewRecommendationEventsHandler(publisher)(ctx)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response recommendationEventBatchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Accepted != 1 || response.Rejected != 1 || response.Results[1].Reason != "invalid_tracking_token" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if publisher.calls != 1 || len(publisher.events) != 1 || publisher.events[0].ID != validID ||
		publisher.events[0].Type != eventing.EventTypeRecommendationImpression {
		t.Fatalf("unexpected published events: %#v", publisher.events)
	}
}

func TestRecommendationEventsHandlerReturns422WithoutKafkaForAllInvalid(t *testing.T) {
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	originalAllow := allowRecommendationTelemetryEvents
	t.Cleanup(func() { allowRecommendationTelemetryEvents = originalAllow })
	allowRecommendationTelemetryEvents = func(uint, int) (bool, error) { return true, nil }
	publisher := &recommendationTestPublisher{}
	ctx, recorder := recommendationTestContext(t, recommendationEventBatchRequest{Events: []recommendationEventInput{{
		EventID: "not-a-uuid", EventType: models.RecommendationEventTypeClick,
	}}}, 7)
	NewRecommendationEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusUnprocessableEntity || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestRecommendationEventsHandlerReturns503OnKafkaError(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	originalAllow, originalNow := allowRecommendationTelemetryEvents, recommendationTelemetryNow
	t.Cleanup(func() { allowRecommendationTelemetryEvents = originalAllow; recommendationTelemetryNow = originalNow })
	allowRecommendationTelemetryEvents = func(uint, int) (bool, error) { return true, nil }
	recommendationTelemetryNow = func() time.Time { return now }
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	ctx, recorder := recommendationTestContext(t, recommendationEventBatchRequest{Events: []recommendationEventInput{{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick, TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}}}, 7)
	NewRecommendationEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
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
	if event.EventType != eventing.EventTypeRecommendationReadEnd || event.Payload.EstimatedReadTimeMS == nil ||
		*event.Payload.EstimatedReadTimeMS != 3000 || event.Payload.ReadPolicyVersion == nil ||
		*event.Payload.ReadPolicyVersion != recommendationReadPolicyVersion || event.Payload.ReadOutcome == nil ||
		*event.Payload.ReadOutcome != recommendationReadOutcomeNeutral {
		t.Fatalf("unexpected validated event: %#v", event)
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

func TestValidateRecommendationTelemetryEventCopiesSignedExplorationProvenance(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	claims := recommendationTrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), ArticleID: 11, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendationResultSelectionExploration), ExplorationReason: recommendationExplorationReasonRecent,
	}
	token, err := signRecommendationTrackingClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: "impression", TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}, now, key)
	if reason != "" {
		t.Fatalf("reason=%q", reason)
	}
	if !event.Payload.ExplorationOpportunity || event.Payload.SelectionMode != string(recommendationResultSelectionExploration) || event.Payload.ExplorationReason != recommendationExplorationReasonRecent {
		t.Fatalf("payload provenance=%#v", event.Payload)
	}
}

func TestValidateRecommendationTelemetryEventCopiesSignedRankedOpportunity(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	claims := recommendationTrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), ArticleID: 11, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendationResultSelectionRanked), ExplorationReason: "",
	}
	token, err := signRecommendationTrackingClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeImpression,
		TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}, now, key)
	if reason != "" {
		t.Fatalf("reason=%q", reason)
	}
	if !event.Payload.ExplorationOpportunity || event.Payload.SelectionMode != string(recommendationResultSelectionRanked) || event.Payload.ExplorationReason != "" {
		t.Fatalf("ranked opportunity payload=%#v", event.Payload)
	}
}

func TestRecommendationEventsHandlerUsesSignedProvenanceAgainstClientFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := "0123456789abcdef0123456789abcdef"
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", key)
	originalAllow, originalNow, originalDB := allowRecommendationTelemetryEvents, recommendationTelemetryNow, global.Db
	t.Cleanup(func() {
		allowRecommendationTelemetryEvents = originalAllow
		recommendationTelemetryNow = originalNow
		global.Db = originalDB
	})
	allowRecommendationTelemetryEvents = func(uint, int) (bool, error) { return true, nil }
	recommendationTelemetryNow = func() time.Time { return now }
	global.Db = nil
	claims := recommendationTrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), ArticleID: 11, Position: 1,
		Scene: recommendationScene, RankerVersion: recommendationRankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendationResultSelectionExploration), ExplorationReason: recommendationExplorationReasonRecent,
	}
	token, err := signRecommendationTrackingClaims(claims, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"events": []map[string]interface{}{{
		"event_id": uuid.NewString(), "event_type": models.RecommendationEventTypeImpression,
		"tracking_token": token, "occurred_at": now.Format(time.RFC3339Nano),
		"exploration_opportunity": false, "selection_mode": string(recommendationResultSelectionRanked), "exploration_reason": "",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/recommendation-events", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	publisher := &recommendationTestPublisher{}
	NewRecommendationEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || len(publisher.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(publisher.events), recorder.Body.String())
	}
	var payload eventing.RecommendationBehaviorPayload
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.ExplorationOpportunity || payload.SelectionMode != string(recommendationResultSelectionExploration) || payload.ExplorationReason != recommendationExplorationReasonRecent {
		t.Fatalf("publisher payload used client provenance instead of signed claims: %#v", payload)
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
		{name: "feed with detail payload", eventType: models.RecommendationEventTypeFeedDwell, feed: &valid, foreground: &foreground, progress: &progress, exitType: &exitType, wantReason: "unexpected_feed_payload"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
			event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
				EventID: uuid.NewString(), EventType: tc.eventType, TrackingToken: token,
				OccurredAt: now.Format(time.RFC3339Nano), ForegroundTimeMS: tc.foreground,
				ScrollProgressPercent: tc.progress, ExitType: tc.exitType, FeedVisibleTimeMS: tc.feed,
			}, now, key)
			if reason != tc.wantReason {
				t.Fatalf("reason=%q want=%q event=%#v", reason, tc.wantReason, event)
			}
			if tc.wantReason == "" && (event.EventType != eventing.EventTypeRecommendationFeedDwell ||
				event.Payload.FeedVisibleTimeMS == nil || *event.Payload.FeedVisibleTimeMS != valid ||
				event.Payload.ForegroundTimeMS != nil || event.Payload.ReadOutcome != nil) {
				t.Fatalf("unexpected feed event=%#v", event)
			}
		})
	}
}
