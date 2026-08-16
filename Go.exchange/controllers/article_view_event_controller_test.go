package controllers

import (
	"bytes"
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

func TestArticleViewEventsHandlerPublishesClientIDWithoutDB(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	originalNow := articleViewTelemetryNow
	t.Cleanup(func() { articleViewTelemetryNow = originalNow })
	articleViewTelemetryNow = func() time.Time { return now }
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

func TestArticleViewEventsHandlerReturns503OnKafkaError(t *testing.T) {
	now := time.Now().UTC()
	originalNow := articleViewTelemetryNow
	t.Cleanup(func() { articleViewTelemetryNow = originalNow })
	articleViewTelemetryNow = func() time.Time { return now }
	publisher := &recommendationTestPublisher{err: errors.New("broker down")}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{{
		EventID: uuid.NewString(), ArticleID: 42, OccurredAt: now.Format(time.RFC3339Nano),
	}}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusServiceUnavailable || publisher.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}

func TestArticleViewEventsHandlerRejectsInvalidInputWithoutKafka(t *testing.T) {
	publisher := &recommendationTestPublisher{}
	ctx, recorder := articleViewTestContext(t, articleViewEventBatchRequest{Events: []articleViewEventInput{{
		EventID: "bad", ArticleID: 0, OccurredAt: "not-a-time",
	}}})
	NewArticleViewEventsHandler(publisher)(ctx)
	if recorder.Code != http.StatusUnprocessableEntity || publisher.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, publisher.calls, recorder.Body.String())
	}
}
