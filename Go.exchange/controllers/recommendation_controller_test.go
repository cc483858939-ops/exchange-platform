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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

type fakeRecommendationResponseMapper struct {
	contexts      []context.Context
	selectedCalls [][]recommendation.SelectedCandidate
	result        []RecommendedPostResponse
	err           error
}

func (fake *fakeRecommendationResponseMapper) Map(ctx context.Context, selected []recommendation.SelectedCandidate, _ time.Time) ([]RecommendedPostResponse, error) {
	fake.contexts = append(fake.contexts, ctx)
	fake.selectedCalls = append(fake.selectedCalls, append([]recommendation.SelectedCandidate(nil), selected...))
	return fake.result, fake.err
}

func newRecommendationHandlerForTest(t *testing.T, service recommendation.Service, mapper RecommendationResponseMapper, timeout time.Duration) *RecommendationHandler {
	t.Helper()
	handler, err := NewRecommendationHandler(service, mapper, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return handler
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
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, time.Second)
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
	if len(mapper.contexts) != 1 || len(mapper.selectedCalls) != 1 {
		t.Fatalf("mapper calls=%d, want one", len(mapper.contexts))
	}
	if _, ok := mapper.contexts[0].Deadline(); !ok {
		t.Fatal("handler did not propagate serving deadline to response mapper")
	}
	if !strings.Contains(recorder.Body.String(), `"request_id":"serving-request"`) || !strings.Contains(recorder.Body.String(), `"depleted":true`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestPublicRecommendationHandlerNormalizesGuestSessionHeader(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{
		RequestID: "guest-request", Now: time.Now().UTC(), Depleted: false,
	}}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
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
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "not-a-uuid")
	handler.GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || len(service.requests) != 1 || service.requests[0].Viewer != (recommendation.Viewer{Kind: recommendation.ViewerGuest}) {
		t.Fatalf("status=%d requests=%#v body=%s", recorder.Code, service.requests, recorder.Body.String())
	}
}

func TestRecommendationHandlerMapsDeadlineAndRequestCancellation(t *testing.T) {
	mapper := &fakeRecommendationResponseMapper{}
	service := &fakeRecommendationService{err: context.DeadlineExceeded}
	handler := newRecommendationHandlerForTest(t, service, mapper, time.Second)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(mapper.contexts) != 0 {
		t.Fatalf("mapper calls=%d, want none after service failure", len(mapper.contexts))
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
	service := &fakeRecommendationService{}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 0 || len(mapper.contexts) != 0 {
		t.Fatalf("unauthenticated request called dependencies: service=%d mapper=%d", len(service.requests), len(mapper.contexts))
	}
}

func TestRecommendationHandlerMapsServiceFailureToHTTP500(t *testing.T) {
	service := &fakeRecommendationService{err: errors.New("service unavailable")}
	mapper := &fakeRecommendationResponseMapper{}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "service unavailable") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.requests) != 1 {
		t.Fatalf("service calls=%d, want one", len(service.requests))
	}
	if len(mapper.contexts) != 0 {
		t.Fatalf("mapper calls=%d, want none after service failure", len(mapper.contexts))
	}
}

func TestRecommendationHandlerMapsTrackingFactsIntoResponseJSON(t *testing.T) {
	recommendations := []RecommendedPostResponse{{Post: postResponse{ID: 17}, Score: .75}}
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
	mapper := &fakeRecommendationResponseMapper{}
	if _, err := NewRecommendationHandler(nil, mapper, 0); err == nil {
		t.Fatal("expected constructor to reject nil service")
	}
	if _, err := NewRecommendationHandler(&fakeRecommendationService{}, nil, 0); err == nil {
		t.Fatal("expected constructor to reject nil response mapper")
	}
}

func TestRecommendationHandlerMapsResponseMapperFailureToHTTP500(t *testing.T) {
	service := &fakeRecommendationService{result: recommendation.ServeResult{RequestID: "request", Now: time.Now().UTC()}}
	mapper := &fakeRecommendationResponseMapper{err: errors.New("hydrate failed")}
	handler := newRecommendationHandlerForTest(t, service, mapper, 0)
	ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/recommendations/posts")
	ctx.Set("user_id", uint(42))
	handler.GetPostRecommendations(ctx)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "hydrate failed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(mapper.contexts) != 1 {
		t.Fatalf("mapper calls=%d, want one", len(mapper.contexts))
	}
}

func TestRecommendationHTTPSpanLinksRequestIDForSuccessAndFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		requestID  string
		serviceErr error
		mapperErr  error
		wantStatus int
	}{
		{name: "success", requestID: "business-request-17", wantStatus: http.StatusOK},
		{name: "service error with request ID", requestID: "failed-request-18", serviceErr: errors.New("service unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "service error without request ID", serviceErr: errors.New("service unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "mapper error", requestID: "mapper-request-19", mapperErr: errors.New("private SQL parameter and token"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown tracer provider: %v", err)
				}
			})
			requestCtx, serverSpan := provider.Tracer("test").Start(context.Background(), "GET /api/public/recommendations/posts")
			service := &fakeRecommendationService{
				result: recommendation.ServeResult{RequestID: test.requestID, Now: time.Now().UTC()},
				err:    test.serviceErr,
			}
			mapper := &fakeRecommendationResponseMapper{err: test.mapperErr}
			handler := newRecommendationHandlerForTest(t, service, mapper, 0)
			ctx, recorder := newRecommendationHTTPContext(http.MethodGet, "/api/public/recommendations/posts")
			ctx.Request = ctx.Request.WithContext(requestCtx)
			handler.GetPublicPostRecommendations(ctx)
			serverSpan.End()

			spans := exporter.GetSpans()
			var server, mapperSpan *tracetest.SpanStub
			for index := range spans {
				switch spans[index].Name {
				case "GET /api/public/recommendations/posts":
					server = &spans[index]
				case "recommendation.response.map":
					mapperSpan = &spans[index]
				}
			}
			if server == nil {
				t.Fatalf("missing server span: %#v", spans)
			}
			if test.serviceErr == nil && mapperSpan == nil {
				t.Fatalf("missing response map span: %#v", spans)
			}
			if mapperSpan != nil && mapperSpan.Parent.SpanID() != server.SpanContext.SpanID() {
				t.Fatalf("response map parent=%s server span=%s", mapperSpan.Parent.SpanID(), server.SpanContext.SpanID())
			}
			if test.requestID != "" {
				if !hasStringAttribute(server.Attributes, "recommendation.request_id", test.requestID) {
					t.Fatalf("business request ID missing from HTTP span: %#v", server.Attributes)
				}
			} else if hasAttribute(server.Attributes, "recommendation.request_id") {
				t.Fatalf("empty request ID created an HTTP attribute: %#v", server.Attributes)
			}
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.serviceErr != nil && !strings.Contains(recorder.Body.String(), test.serviceErr.Error()) {
				t.Fatalf("service error response changed: %s", recorder.Body.String())
			}
			if test.mapperErr != nil {
				if mapperSpan.Status.Code != codes.Error {
					t.Fatalf("mapper status=%v, want error", mapperSpan.Status.Code)
				}
				for _, event := range mapperSpan.Events {
					for _, item := range event.Attributes {
						if strings.Contains(item.Value.AsString(), "private SQL") || strings.Contains(item.Value.AsString(), "token") {
							t.Fatalf("sensitive mapper error leaked into trace: %#v", event.Attributes)
						}
					}
				}
			}
		})
	}
}

func hasAttribute(attributes []attribute.KeyValue, key string) bool {
	for _, item := range attributes {
		if string(item.Key) == key {
			return true
		}
	}
	return false
}

func hasStringAttribute(attributes []attribute.KeyValue, key, want string) bool {
	for _, item := range attributes {
		if string(item.Key) == key && item.Value.AsString() == want {
			return true
		}
	}
	return false
}
