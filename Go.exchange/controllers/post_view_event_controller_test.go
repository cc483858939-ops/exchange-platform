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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func postViewTestContext(t *testing.T, request postViewEventBatchRequest) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/post-view-events", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, recorder
}

func allowPostViewTestEvents(t *testing.T, allow func(uint, int) (bool, error)) {
	t.Helper()
	original := allowPostViewEvents
	t.Cleanup(func() { allowPostViewEvents = original })
	allowPostViewEvents = allow
}

func validPostViewInput(now time.Time) postViewEventInput {
	return postViewEventInput{
		EventID: uuid.NewString(), PostID: 42, OccurredAt: now.Format(time.RFC3339Nano), Source: "post_detail",
	}
}

func TestPostViewEventsHandlerPublishesClientIDWithoutDB(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	originalNow := postViewTelemetryNow
	t.Cleanup(func() { postViewTelemetryNow = originalNow })
	postViewTelemetryNow = func() time.Time { return now }
	allowPostViewTestEvents(t, func(_ uint, eventCount int) (bool, error) {
		if eventCount != 1 {
			t.Fatalf("eventCount=%d want=1", eventCount)
		}
		return true, nil
	})

	id := uuid.NewString()
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{{
		EventID: id, PostID: 42, OccurredAt: now.Format(time.RFC3339Nano), Source: "post_detail",
	}}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || publisher.calls != 1 || len(publisher.events) != 1 {
		t.Fatalf("status=%d calls=%d events=%#v body=%s", recorder.Code, publisher.calls, publisher.events, recorder.Body.String())
	}
	if publisher.events[0].ID != id || publisher.events[0].Type != eventing.EventTypePostViewed ||
		eventing.KeyForEvent(publisher.events[0]) != "7" {
		t.Fatalf("published event=%#v", publisher.events[0])
	}
	var payload eventing.UserBehaviorPayload
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "post_detail" {
		t.Fatalf("published source=%q", payload.Source)
	}
}

func TestPostViewEventsHandlerPublishesFeedSource(t *testing.T) {
	now := time.Now().UTC()
	allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
	publisher := &recommendationTestPublisher{}
	input := validPostViewInput(now)
	input.Source = "feed"
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{input}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || len(publisher.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(publisher.events), recorder.Body.String())
	}
	var payload eventing.UserBehaviorPayload
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "feed" {
		t.Fatalf("published source=%q", payload.Source)
	}
}

func TestPostViewEventsHandlerRejectsMissingAndUnknownSource(t *testing.T) {
	for _, source := range []string{"", "unknown"} {
		t.Run(source, func(t *testing.T) {
			now := time.Now().UTC()
			allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
			input := validPostViewInput(now)
			input.Source = source
			publisher := &recommendationTestPublisher{}
			ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{input}})
			NewPostViewEventsHandler(publisher)(ctx)
			if recorder.Code != http.StatusUnprocessableEntity || publisher.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
			}
			var response postViewEventBatchResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Results) != 1 || response.Results[0].Reason != "invalid_source" {
				t.Fatalf("response=%#v", response)
			}
		})
	}
}

func TestPostViewEventsHandlerLimiterReceivesEventCount(t *testing.T) {
	now := time.Now().UTC()
	var gotUserID, gotEventCount uint
	allowPostViewTestEvents(t, func(userID uint, eventCount int) (bool, error) {
		gotUserID = userID
		gotEventCount = uint(eventCount)
		return true, nil
	})
	events := make([]postViewEventInput, 50)
	for index := range events {
		events[index] = validPostViewInput(now)
	}
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: events})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
	if gotUserID != 7 || gotEventCount != 50 {
		t.Fatalf("limiter received userID=%d eventCount=%d", gotUserID, gotEventCount)
	}
	if len(publisher.events) != 50 {
		t.Fatalf("published=%d want=50", len(publisher.events))
	}
}

func TestPostViewEventsHandlerReturns429WithoutKafkaWhenOverLimit(t *testing.T) {
	allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return false, nil })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{validPostViewInput(time.Now().UTC())}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "60" || publisher.calls != 0 {
		t.Fatalf("status=%d retry-after=%q calls=%d body=%s", recorder.Code, recorder.Header().Get("Retry-After"), publisher.calls, recorder.Body.String())
	}
}

func TestPostViewEventsHandlerReturns503WithoutKafkaWhenLimiterFails(t *testing.T) {
	allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return false, errors.New("redis unavailable") })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{validPostViewInput(time.Now().UTC())}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestPostViewEventsHandlerRateLimitsBeforeIndividualValidation(t *testing.T) {
	var gotEventCount int
	allowPostViewTestEvents(t, func(_ uint, eventCount int) (bool, error) {
		gotEventCount = eventCount
		return true, nil
	})
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{{
		EventID: "bad", PostID: 0, OccurredAt: "not-a-time",
	}}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusUnprocessableEntity || gotEventCount != 1 || publisher.calls != 0 {
		t.Fatalf("status=%d eventCount=%d calls=%d body=%s", recorder.Code, gotEventCount, publisher.calls, recorder.Body.String())
	}
}

func TestPostViewEventsHandlerReturns503OnKafkaError(t *testing.T) {
	now := time.Now().UTC()
	allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{validPostViewInput(now)}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestPostViewEventsHandlerUsesInjectedPublisherWhenGlobalDBNil(t *testing.T) {
	now := time.Now().UTC()
	allowPostViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := postViewTestContext(t, postViewEventBatchRequest{Events: []postViewEventInput{validPostViewInput(now)}})
	NewPostViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

var _ eventing.BatchPublisher = (*recommendationTestPublisher)(nil)
var _ context.Context
