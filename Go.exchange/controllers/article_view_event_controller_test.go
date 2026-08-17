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

func articleViewTestContext(t *testing.T, request articleViewEventBatchRequest) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/article-view-events", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, recorder
}

func allowArticleViewTestEvents(t *testing.T, allow func(uint, int) (bool, error)) {
	t.Helper()
	original := allowArticleViewEvents
	t.Cleanup(func() { allowArticleViewEvents = original })
	allowArticleViewEvents = allow
}

func validArticleViewInput(now time.Time) articleViewEventInput {
	return articleViewEventInput{
		EventID: uuid.NewString(), ArticleID: 42, OccurredAt: now.Format(time.RFC3339Nano),
	}
}

func TestArticleViewEventsHandlerPublishesClientIDWithoutDB(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	originalNow := articleViewTelemetryNow
	t.Cleanup(func() { articleViewTelemetryNow = originalNow })
	articleViewTelemetryNow = func() time.Time { return now }
	allowArticleViewTestEvents(t, func(_ uint, eventCount int) (bool, error) {
		if eventCount != 1 {
			t.Fatalf("eventCount=%d want=1", eventCount)
		}
		return true, nil
	})

	id := uuid.NewString()
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{{
		EventID: id, ArticleID: 42, OccurredAt: now.Format(time.RFC3339Nano),
	}}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || publisher.calls != 1 || len(publisher.events) != 1 {
		t.Fatalf("status=%d calls=%d events=%#v body=%s", recorder.Code, publisher.calls, publisher.events, recorder.Body.String())
	}
	if publisher.events[0].ID != id || publisher.events[0].Type != eventing.EventTypeArticleViewed ||
		eventing.KeyForEvent(publisher.events[0]) != "7" {
		t.Fatalf("published event=%#v", publisher.events[0])
	}
}

func TestArticleViewEventsHandlerLimiterReceivesEventCount(t *testing.T) {
	now := time.Now().UTC()
	var gotUserID, gotEventCount uint
	allowArticleViewTestEvents(t, func(userID uint, eventCount int) (bool, error) {
		gotUserID = userID
		gotEventCount = uint(eventCount)
		return true, nil
	})
	events := make([]articleViewEventInput, 50)
	for index := range events {
		events[index] = validArticleViewInput(now)
	}
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: events})
	NewArticleViewEventsHandler(publisher)(ctx)
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

func TestArticleViewEventsHandlerReturns429WithoutKafkaWhenOverLimit(t *testing.T) {
	allowArticleViewTestEvents(t, func(_ uint, _ int) (bool, error) { return false, nil })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{validArticleViewInput(time.Now().UTC())}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "60" || publisher.calls != 0 {
		t.Fatalf("status=%d retry-after=%q calls=%d body=%s", recorder.Code, recorder.Header().Get("Retry-After"), publisher.calls, recorder.Body.String())
	}
}

func TestArticleViewEventsHandlerReturns503WithoutKafkaWhenLimiterFails(t *testing.T) {
	allowArticleViewTestEvents(t, func(_ uint, _ int) (bool, error) { return false, errors.New("redis unavailable") })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{validArticleViewInput(time.Now().UTC())}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestArticleViewEventsHandlerRateLimitsBeforeIndividualValidation(t *testing.T) {
	var gotEventCount int
	allowArticleViewTestEvents(t, func(_ uint, eventCount int) (bool, error) {
		gotEventCount = eventCount
		return true, nil
	})
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{{
		EventID: "bad", ArticleID: 0, OccurredAt: "not-a-time",
	}}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusUnprocessableEntity || gotEventCount != 1 || publisher.calls != 0 {
		t.Fatalf("status=%d eventCount=%d calls=%d body=%s", recorder.Code, gotEventCount, publisher.calls, recorder.Body.String())
	}
}

func TestArticleViewEventsHandlerReturns503OnKafkaError(t *testing.T) {
	now := time.Now().UTC()
	allowArticleViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{validArticleViewInput(now)}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestArticleViewEventsHandlerUsesInjectedPublisherWhenGlobalDBNil(t *testing.T) {
	now := time.Now().UTC()
	allowArticleViewTestEvents(t, func(_ uint, _ int) (bool, error) { return true, nil })
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{validArticleViewInput(now)}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusAccepted || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

var _ eventing.BatchPublisher = (*recommendationTestPublisher)(nil)
var _ context.Context
