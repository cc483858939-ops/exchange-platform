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
	"Go.exchange/models"
	"Go.exchange/recommendation"

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
	if p.err != nil {
		return p.err
	}
	p.events = append(p.events, events...)
	return nil
}

type recommendationTestCooldownKey struct {
	userID    uint
	postID    uint
	requestID string
}

type recommendationTestCooldownEntry struct {
	eventID   string
	committed bool
	expiresAt time.Time
}

type recommendationTestCooldown struct {
	entries      map[recommendationTestCooldownKey]recommendationTestCooldownEntry
	pendingTTL   time.Duration
	cooldownTTL  time.Duration
	now          func() time.Time
	acquireCalls []recommendationTestCooldownKey
	commitCalls  []recommendationTestCooldownKey
	releaseCalls []recommendationTestCooldownKey
	acquireErrAt int
	acquireErr   error
	commitErr    error
	releaseErr   error
}

func newRecommendationTestCooldown(now func() time.Time) *recommendationTestCooldown {
	return &recommendationTestCooldown{
		entries:    make(map[recommendationTestCooldownKey]recommendationTestCooldownEntry),
		pendingTTL: recommendationClickCooldownPendingTTL, cooldownTTL: 5 * time.Minute, now: now,
	}
}

func (store *recommendationTestCooldown) Acquire(_ context.Context, userID, postID uint, requestID, eventID string) (RecommendationClickCooldownAcquireResult, error) {
	key := recommendationTestCooldownKey{userID: userID, postID: postID, requestID: requestID}
	store.acquireCalls = append(store.acquireCalls, key)
	if store.acquireErrAt > 0 && len(store.acquireCalls) == store.acquireErrAt {
		return 0, store.acquireErr
	}
	entry, exists := store.entries[key]
	if exists && !entry.expiresAt.After(store.now()) {
		delete(store.entries, key)
		exists = false
	}
	if !exists {
		store.entries[key] = recommendationTestCooldownEntry{eventID: eventID, expiresAt: store.now().Add(store.pendingTTL)}
		return RecommendationClickCooldownAcquired, nil
	}
	if entry.committed {
		return RecommendationClickCooldownDuplicate, nil
	}
	return RecommendationClickCooldownPending, nil
}

func (store *recommendationTestCooldown) Commit(_ context.Context, userID, postID uint, requestID, eventID string) error {
	key := recommendationTestCooldownKey{userID: userID, postID: postID, requestID: requestID}
	store.commitCalls = append(store.commitCalls, key)
	if store.commitErr != nil {
		return store.commitErr
	}
	entry, exists := store.entries[key]
	if !exists || entry.committed || entry.eventID != eventID || !entry.expiresAt.After(store.now()) {
		return ErrRecommendationClickCooldownOwnershipLost
	}
	entry.committed = true
	entry.expiresAt = store.now().Add(store.cooldownTTL)
	store.entries[key] = entry
	return nil
}

func (store *recommendationTestCooldown) Release(_ context.Context, userID, postID uint, requestID, eventID string) error {
	key := recommendationTestCooldownKey{userID: userID, postID: postID, requestID: requestID}
	store.releaseCalls = append(store.releaseCalls, key)
	if store.releaseErr != nil {
		return store.releaseErr
	}
	entry, exists := store.entries[key]
	if exists && !entry.committed && entry.eventID == eventID {
		delete(store.entries, key)
	}
	return nil
}

func allowRecommendationTelemetryTestRateLimit(uint, int) (bool, error) { return true, nil }

func setRecommendationTelemetryTestClock(t *testing.T, now *time.Time) {
	t.Helper()
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	previous := recommendationTelemetryNow
	t.Cleanup(func() { recommendationTelemetryNow = previous })
	recommendationTelemetryNow = func() time.Time { return *now }
}

func recommendationTestInput(eventID, eventType, token string, occurredAt time.Time) recommendationEventInput {
	return recommendationEventInput{
		EventID: eventID, EventType: eventType, TrackingToken: token, OccurredAt: occurredAt.Format(time.RFC3339Nano),
	}
}

func recommendationTestKey(t *testing.T, userID, postID uint, token string) recommendationTestCooldownKey {
	t.Helper()
	claims, err := recommendation.VerifyTrackingToken(token, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return recommendationTestCooldownKey{userID: userID, postID: postID, requestID: claims.RequestID}
}

func signTestRecommendationToken(t *testing.T, userID, postID uint, now time.Time, estimated int64, policy string) string {
	t.Helper()
	token, err := recommendation.SignTrackingClaims(recommendation.TrackingClaims{
		UserID: userID, RequestID: uuid.NewString(), PostID: postID, Position: 1,
		Scene: recommendation.RecommendationScene, RankerVersion: recommendation.RankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendation.RecommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: estimated, ReadPolicyVersion: policy,
		SelectionMode: string(recommendation.SelectionModeRanked),
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
	originalNow := recommendationTelemetryNow
	t.Cleanup(func() {
		recommendationTelemetryNow = originalNow
	})
	recommendationTelemetryNow = func() time.Time { return now }

	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	validID := uuid.NewString()
	request := recommendationEventBatchRequest{Events: []recommendationEventInput{
		{EventID: validID, EventType: models.RecommendationEventTypeImpression, TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano)},
		{EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick, TrackingToken: token + "tampered", OccurredAt: now.Format(time.RFC3339Nano)},
	}}
	publisher := &recommendationTestPublisher{}
	ctx, recorder := recommendationTestContext(t, request, 7)
	NewRecommendationEventsHandler(publisher, allowRecommendationTelemetryTestRateLimit)(ctx)

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
	publisher := &recommendationTestPublisher{}
	ctx, recorder := recommendationTestContext(t, recommendationEventBatchRequest{Events: []recommendationEventInput{{
		EventID: "not-a-uuid", EventType: models.RecommendationEventTypeClick,
	}}}, 7)
	NewRecommendationEventsHandler(publisher, allowRecommendationTelemetryTestRateLimit)(ctx)
	if recorder.Code != http.StatusUnprocessableEntity || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestRecommendationEventsHandlerReturns503OnKafkaError(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	ctx, recorder := recommendationTestContext(t, recommendationEventBatchRequest{Events: []recommendationEventInput{{
		EventID: uuid.NewString(), EventType: models.RecommendationEventTypeClick, TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}}}, 7)
	NewRecommendationEventsHandler(publisher, allowRecommendationTelemetryTestRateLimit, cooldown)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 1 || len(cooldown.releaseCalls) != 1 {
		t.Fatalf("status=%d calls=%d releases=%d body=%s", recorder.Code, publisher.calls, len(cooldown.releaseCalls), recorder.Body.String())
	}
}

func submitRecommendationTestBatch(t *testing.T, publisher eventing.BatchPublisher, cooldown RecommendationClickCooldown, userID uint, events []recommendationEventInput) (int, recommendationEventBatchResponse) {
	t.Helper()
	ctx, recorder := recommendationTestContext(t, recommendationEventBatchRequest{Events: events}, userID)
	NewRecommendationEventsHandler(publisher, allowRecommendationTelemetryTestRateLimit, cooldown)(ctx)
	var response recommendationEventBatchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recommendation response %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, response
}

func TestRecommendationClickCooldownFiveMinutes(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{}
	firstToken := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	firstEventID := uuid.NewString()
	status, response := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(firstEventID, models.RecommendationEventTypeClick, firstToken, now),
	})
	if status != http.StatusAccepted || response.Accepted != 1 || response.Duplicates != 0 || len(publisher.events) != 1 {
		t.Fatalf("first click status=%d response=%#v published=%d", status, response, len(publisher.events))
	}
	key := cooldown.acquireCalls[0]
	firstExpiry := cooldown.entries[key].expiresAt

	now = now.Add(time.Minute)
	secondToken := firstToken
	status, response = submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, secondToken, now),
	})
	if status != http.StatusAccepted || response.Accepted != 0 || response.Duplicates != 1 || len(publisher.events) != 1 {
		t.Fatalf("click within cooldown status=%d response=%#v published=%d", status, response, len(publisher.events))
	}
	if got := cooldown.entries[key].expiresAt; !got.Equal(firstExpiry) {
		t.Fatalf("duplicate refreshed cooldown expiry from %s to %s", firstExpiry, got)
	}

	now = firstExpiry
	thirdToken := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	status, response = submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, thirdToken, now),
	})
	if status != http.StatusAccepted || response.Accepted != 1 || response.Duplicates != 0 || len(publisher.events) != 2 {
		t.Fatalf("click after cooldown status=%d response=%#v published=%d", status, response, len(publisher.events))
	}
}

func TestRecommendationClickCooldownIsScopedToUserAndPost(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{}
	for _, test := range []struct{ userID, postID uint }{{7, 11}, {8, 11}, {7, 12}} {
		token := signTestRecommendationToken(t, test.userID, test.postID, now, 3000, recommendationReadPolicyVersion)
		status, response := submitRecommendationTestBatch(t, publisher, cooldown, test.userID, []recommendationEventInput{
			recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now),
		})
		if status != http.StatusAccepted || response.Accepted != 1 || response.Duplicates != 0 {
			t.Fatalf("user=%d post=%d status=%d response=%#v", test.userID, test.postID, status, response)
		}
	}
	if len(publisher.events) != 3 {
		t.Fatalf("published clicks=%d want 3", len(publisher.events))
	}
}

func TestRecommendationClickCooldownIgnoresNewEventIDReplay(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{}
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	for index, eventID := range []string{uuid.NewString(), uuid.NewString()} {
		status, response := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
			recommendationTestInput(eventID, models.RecommendationEventTypeClick, token, now),
		})
		if status != http.StatusAccepted {
			t.Fatalf("replay %d status=%d response=%#v", index, status, response)
		}
		if index == 0 && (response.Accepted != 1 || response.Duplicates != 0) {
			t.Fatalf("first event response=%#v", response)
		}
		if index == 1 && (response.Accepted != 0 || response.Duplicates != 1 || response.Results[0].Reason != "click_cooldown") {
			t.Fatalf("new EventID replay response=%#v", response)
		}
	}
	if publisher.calls != 1 || len(publisher.events) != 1 {
		t.Fatalf("publisher calls=%d events=%d want one", publisher.calls, len(publisher.events))
	}
}

func TestRecommendationClickCooldownAllDuplicatesReturnAcceptedWithoutKafka(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	cooldown.entries[recommendationTestKey(t, 7, 11, token)] = recommendationTestCooldownEntry{
		eventID: "already-published", committed: true, expiresAt: now.Add(5 * time.Minute),
	}
	status, response := submitRecommendationTestBatch(t, nil, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now),
	})
	if status != http.StatusAccepted || response.Accepted != 0 || response.Duplicates != 1 || response.Rejected != 0 {
		t.Fatalf("status=%d response=%#v", status, response)
	}
}

func TestRecommendationClickCooldownReleasesOnPublishFailure(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	event := recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now)
	status, _ := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{event})
	if status != http.StatusServiceUnavailable || len(cooldown.releaseCalls) != 1 || len(cooldown.entries) != 0 {
		t.Fatalf("failed publish status=%d releases=%d reservations=%v", status, len(cooldown.releaseCalls), cooldown.entries)
	}
	publisher.err = nil
	status, response := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{event})
	if status != http.StatusAccepted || response.Accepted != 1 || publisher.calls != 2 || len(publisher.events) != 1 {
		t.Fatalf("retry status=%d response=%#v calls=%d published=%d", status, response, publisher.calls, len(publisher.events))
	}
}

func TestRecommendationClickCooldownRedisFailure(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	cooldown.acquireErrAt = 2
	cooldown.acquireErr = errors.New("redis unavailable")
	publisher := &recommendationTestPublisher{}
	firstToken := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	secondToken := signTestRecommendationToken(t, 7, 12, now, 3000, recommendationReadPolicyVersion)
	status, _ := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, firstToken, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, secondToken, now),
	})
	if status != http.StatusServiceUnavailable || publisher.calls != 0 || len(cooldown.entries) != 0 || len(cooldown.releaseCalls) != 1 {
		t.Fatalf("status=%d publisher calls=%d remaining=%v releases=%d", status, publisher.calls, cooldown.entries, len(cooldown.releaseCalls))
	}
}

func TestRecommendationClickCooldownCommitFailureAfterPublishReturnsAccepted(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	cooldown.commitErr = errors.New("redis commit unavailable")
	publisher := &recommendationTestPublisher{}
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	status, response := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now),
	})
	if status != http.StatusAccepted || response.Accepted != 1 || publisher.calls != 1 || len(publisher.events) != 1 {
		t.Fatalf("status=%d response=%#v publisher calls=%d events=%d", status, response, publisher.calls, len(publisher.events))
	}
}

func TestRecommendationClickCooldownPendingConflictReleasesBatch(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{}
	firstToken := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	secondToken := signTestRecommendationToken(t, 7, 12, now, 3000, recommendationReadPolicyVersion)
	cooldown.entries[recommendationTestKey(t, 7, 12, secondToken)] = recommendationTestCooldownEntry{
		eventID: "other-owner", expiresAt: now.Add(time.Minute),
	}
	status, _ := submitRecommendationTestBatch(t, publisher, cooldown, 7, []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, firstToken, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, secondToken, now),
	})
	if status != http.StatusServiceUnavailable || publisher.calls != 0 || len(cooldown.releaseCalls) != 1 {
		t.Fatalf("status=%d publisher calls=%d releases=%d", status, publisher.calls, len(cooldown.releaseCalls))
	}
	if _, exists := cooldown.entries[recommendationTestKey(t, 7, 11, firstToken)]; exists {
		t.Fatal("batch conflict leaked the earlier reservation")
	}
}

func TestRecommendationEventBatchFiltersOnlyDuplicateClicks(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	setRecommendationTelemetryTestClock(t, &now)
	cooldown := newRecommendationTestCooldown(func() time.Time { return now })
	publisher := &recommendationTestPublisher{}
	token := signTestRecommendationToken(t, 7, 11, now, 3000, recommendationReadPolicyVersion)
	foreground, progress, exitType, feedVisible := int64(2000), 100, "route_leave", int64(1500)
	events := []recommendationEventInput{
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeImpression, token, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeClick, token, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeReadEnd, token, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeFeedDwell, token, now),
		recommendationTestInput(uuid.NewString(), models.RecommendationEventTypeNotInterested, token, now),
	}
	events[3].ForegroundTimeMS = &foreground
	events[3].ScrollProgressPercent = &progress
	events[3].ExitType = &exitType
	events[4].FeedVisibleTimeMS = &feedVisible
	status, response := submitRecommendationTestBatch(t, publisher, cooldown, 7, events)
	if status != http.StatusAccepted || response.Accepted != 5 || response.Duplicates != 1 || response.Rejected != 0 {
		t.Fatalf("status=%d response=%#v", status, response)
	}
	wantStatuses := []string{"accepted", "accepted", "duplicate", "accepted", "accepted", "accepted"}
	for index, want := range wantStatuses {
		if response.Results[index].EventID != events[index].EventID || response.Results[index].Status != want {
			t.Errorf("result[%d]=%#v want event=%s status=%s", index, response.Results[index], events[index].EventID, want)
		}
	}
	if response.Results[2].Reason != "click_cooldown" {
		t.Errorf("duplicate reason=%q want click_cooldown", response.Results[2].Reason)
	}
	wantTypes := []string{
		eventing.EventTypeRecommendationImpression,
		eventing.EventTypeRecommendationClick,
		eventing.EventTypeRecommendationReadEnd,
		eventing.EventTypeRecommendationFeedDwell,
		eventing.EventTypeRecommendationNotInterested,
	}
	if len(publisher.events) != len(wantTypes) {
		t.Fatalf("published events=%d want %d", len(publisher.events), len(wantTypes))
	}
	for index, want := range wantTypes {
		if publisher.events[index].Type != want {
			t.Errorf("published event[%d] type=%q want %q", index, publisher.events[index].Type, want)
		}
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
	claims := recommendation.TrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), PostID: 11, Position: 1,
		Scene: recommendation.RecommendationScene, RankerVersion: recommendation.RankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendation.RecommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendation.SelectionModeExploration), ExplorationReason: recommendation.ExplorationReasonRecent,
	}
	token, err := recommendation.SignTrackingClaims(claims, key)
	if err != nil {
		t.Fatal(err)
	}
	event, reason := validateRecommendationTelemetryEvent(7, recommendationEventInput{
		EventID: uuid.NewString(), EventType: "impression", TrackingToken: token, OccurredAt: now.Format(time.RFC3339Nano),
	}, now, key)
	if reason != "" {
		t.Fatalf("reason=%q", reason)
	}
	if !event.Payload.ExplorationOpportunity || event.Payload.SelectionMode != string(recommendation.SelectionModeExploration) || event.Payload.ExplorationReason != recommendation.ExplorationReasonRecent {
		t.Fatalf("payload provenance=%#v", event.Payload)
	}
}

func TestValidateRecommendationTelemetryEventCopiesSignedRankedOpportunity(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	claims := recommendation.TrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), PostID: 11, Position: 1,
		Scene: recommendation.RecommendationScene, RankerVersion: recommendation.RankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendation.RecommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendation.SelectionModeRanked), ExplorationReason: "",
	}
	token, err := recommendation.SignTrackingClaims(claims, key)
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
	if !event.Payload.ExplorationOpportunity || event.Payload.SelectionMode != string(recommendation.SelectionModeRanked) || event.Payload.ExplorationReason != "" {
		t.Fatalf("ranked opportunity payload=%#v", event.Payload)
	}
}

func TestRecommendationEventsHandlerUsesSignedProvenanceAgainstClientFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	key := "0123456789abcdef0123456789abcdef"
	t.Setenv("RECOMMENDATION_TELEMETRY_ENABLED", "true")
	t.Setenv("RECOMMENDATION_TELEMETRY_SIGNING_KEY", key)
	originalNow := recommendationTelemetryNow
	t.Cleanup(func() {
		recommendationTelemetryNow = originalNow
	})
	recommendationTelemetryNow = func() time.Time { return now }
	claims := recommendation.TrackingClaims{
		UserID: 7, RequestID: uuid.NewString(), PostID: 11, Position: 1,
		Scene: recommendation.RecommendationScene, RankerVersion: recommendation.RankerVersion,
		RankerConfigHash: "0123456789ab", StrategyID: recommendation.RecommendationPersonalizedStrategyID,
		IssuedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(time.Hour).Unix(),
		EstimatedReadTimeMS: 3000, ReadPolicyVersion: recommendationReadPolicyVersion,
		ExplorationOpportunity: true, SelectionMode: string(recommendation.SelectionModeExploration), ExplorationReason: recommendation.ExplorationReasonRecent,
	}
	token, err := recommendation.SignTrackingClaims(claims, []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"events": []map[string]interface{}{{
		"event_id": uuid.NewString(), "event_type": models.RecommendationEventTypeImpression,
		"tracking_token": token, "occurred_at": now.Format(time.RFC3339Nano),
		"exploration_opportunity": false, "selection_mode": string(recommendation.SelectionModeRanked), "exploration_reason": "",
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
	NewRecommendationEventsHandler(publisher, allowRecommendationTelemetryTestRateLimit)(ctx)
	if recorder.Code != http.StatusAccepted || len(publisher.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(publisher.events), recorder.Body.String())
	}
	var payload eventing.RecommendationBehaviorPayload
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.ExplorationOpportunity || payload.SelectionMode != string(recommendation.SelectionModeExploration) || payload.ExplorationReason != recommendation.ExplorationReasonRecent {
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
