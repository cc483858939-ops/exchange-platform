package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
)

type recommendationServiceStub struct {
	request recommendation.ServeRequest
	result  recommendation.ServeResult
	err     error
}

func (stub *recommendationServiceStub) Serve(ctx context.Context, request recommendation.ServeRequest) (recommendation.ServeResult, error) {
	stub.request = request
	return stub.result, stub.err
}

func newRecommendationHTTPContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	return ctx, recorder
}

func TestRecommendationHandlerUsesInjectedServiceAndMapsRequest(t *testing.T) {
	service := &recommendationServiceStub{result: recommendation.ServeResult{
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
	if service.request.Viewer != (recommendation.Viewer{Kind: recommendation.ViewerAuthenticated, UserID: 42}) {
		t.Fatalf("viewer=%#v", service.request.Viewer)
	}
	if service.request.Limit != 77 {
		t.Fatalf("limit=%d want raw transport value 77 for Service policy", service.request.Limit)
	}
	if service.request.BrowserLanguage.BrowserPrimary != "ja" {
		t.Fatalf("browser language=%#v", service.request.BrowserLanguage)
	}
	if !strings.Contains(recorder.Body.String(), `"request_id":"serving-request"`) || !strings.Contains(recorder.Body.String(), `"depleted":true`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestPublicRecommendationHandlerNormalizesGuestSessionHeader(t *testing.T) {
	service := &recommendationServiceStub{result: recommendation.ServeResult{
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
	if service.request.Viewer.Kind != recommendation.ViewerGuest || service.request.Viewer.GuestSessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
		t.Fatalf("viewer=%#v", service.request.Viewer)
	}
	if service.request.Limit != 0 {
		t.Fatalf("invalid limit=%d want service default sentinel 0", service.request.Limit)
	}
}

func TestRecommendationHandlerMapsDeadlineAndRequestCancellation(t *testing.T) {
	handler, err := NewRecommendationHandler(&recommendationServiceStub{err: context.DeadlineExceeded}, nil, time.Second)
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
	handler, err := NewRecommendationHandler(&recommendationServiceStub{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
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
