package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
)

type fakeRecommendationService struct {
	requests []recommendation.ServeRequest
	contexts []context.Context
	result   recommendation.ServeResult
	err      error
}

func (fake *fakeRecommendationService) Serve(ctx context.Context, request recommendation.ServeRequest) (recommendation.ServeResult, error) {
	fake.requests = append(fake.requests, request)
	fake.contexts = append(fake.contexts, ctx)
	return fake.result, fake.err
}

func newRecommendationHTTPContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	return ctx, recorder
}

func TestRecommendationHandlerUsesInjectedServiceAndMapsRequest(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "serving-request", Now: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC), Depleted: true,
	}}
	handler, err := NewRecommendationHandler(service, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts?limit=77")
	ctx.Set("user_id", uint(42))
	ctx.Request.Header.Set("Accept-Language", "ja-JP, en-US;q=0.2")
	handler.GetPostRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 || service.requests[0].Viewer != (recommendation.Viewer{Kind: recommendation.ViewerAuthenticated, UserID: 42}) {
		t.Fatalf("requests=%#v", service.requests)
	}
	if service.requests[0].Limit != 77 {
		t.Fatalf("limit=%d want raw transport value 77 for Service policy", service.requests[0].Limit)
	}
	if service.requests[0].BrowserLanguage.BrowserPrimary != "ja" {
		t.Fatalf("browser language=%#v", service.requests[0].BrowserLanguage)
	}
	if len(service.contexts) != 1 {
		t.Fatalf("service calls=%d, want one", len(service.contexts))
	}
	if _, ok := service.contexts[0].Deadline(); !ok {
		t.Fatal("handler did not propagate its configured serving deadline")
	}
	if !strings.Contains(recorder.Body.String(), `"request_id":"serving-request"`) || !strings.Contains(recorder.Body.String(), `"depleted":true`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestPublicRecommendationHandlerNormalizesGuestSessionHeader(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "guest-request", Now: time.Now().UTC(), Depleted: false,
	}}
	handler, err := NewRecommendationHandler(service, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts?limit=not-a-number")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "  4CA3706B-197E-4F63-8F51-F99176F8B61C ")
	handler.GetPublicPostRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 || service.requests[0].Viewer.Kind != recommendation.ViewerGuest || service.requests[0].Viewer.GuestSessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
		t.Fatalf("requests=%#v", service.requests)
	}
	if service.requests[0].Limit != 0 {
		t.Fatalf("invalid limit=%d want service default sentinel 0", service.requests[0].Limit)
	}
}

func TestPublicRecommendationHandlerKeepsInvalidGuestSessionEmpty(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{RequestID: "guest-request", Now: time.Now().UTC()}}
	handler, err := NewRecommendationHandler(service, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "not-a-uuid")
	handler.GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || len(service.requests) != 1 || service.requests[0].Viewer != (recommendation.Viewer{Kind: recommendation.ViewerGuest}) {
		t.Fatalf("status=%d requests=%#v body=%s", recorder.Code, service.requests, recorder.Body.String())
	}
}

func TestRecommendationHandlerMapsDeadlineAndRequestCancellation(t *testing.T) {
	handler, err := NewRecommendationHandler(&fakeRecommendationService{err: context.DeadlineExceeded}, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, recorder = newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	ctx.Request = ctx.Request.WithContext(cancelled)
	handler.GetPostRecommendations(ctx)
	if ctx.Writer.Written() {
		t.Fatalf("canceled parent request wrote a response: %s", recorder.Body.String())
	}
}

func TestRecommendationHandlerRequiresAuthenticatedViewer(t *testing.T) {
	handler, err := NewRecommendationHandler(&fakeRecommendationService{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRecommendationHandlerMapsServiceFailureToHTTP500(t *testing.T) {
	service := &fakeRecommendationService{err: errors.New("service unavailable")}
	handler, err := NewRecommendationHandler(service, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "service unavailable") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 {
		t.Fatalf("service calls=%d, want one", len(service.requests))
	}
}

func TestRecommendationHandlerMapsTrackingFactsIntoResponseJSON(t *testing.T) {
	recommendations := []recommendedPostResponse{{Post: postResponse{ID: 17}, Score: .75}}
	facts := []recommendation.TrackingFact{{
		PostID: 17, RequestID: "request-17", Position: 1, Scene: "recommendation_page",
		RankerVersion: "rules_v6", RankerConfigHash: "hash-17", StrategyID: "strategy-17",
		Token: "signed-token", ExpiresAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
	}}
	attachRecommendationTrackingFacts(recommendations, facts)
	body, err := json.Marshal(postRecommendationPageResponse{Items: recommendations, RequestID: "request-17"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"tracking"`, `"request_id":"request-17"`, `"position":1`, `"token":"signed-token"`, `"ranker_version":"rules_v6"`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("response=%s missing %s", body, field)
		}
	}
}

func TestRecommendationHandlerRequiresService(t *testing.T) {
	if _, err := NewRecommendationHandler(nil, nil, 0); err == nil {
		t.Fatal("expected constructor to reject nil service")
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
