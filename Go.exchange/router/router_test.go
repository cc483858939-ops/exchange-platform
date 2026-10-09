package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSetupRouterIgnoresForwardedHeadersWithoutTrustedProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "203.0.113.10:1234", "1.2.3.4", "")
	if got := strings.TrimSpace(response.Body.String()); got != "203.0.113.10" {
		t.Fatalf("ClientIP=%q, want direct source", got)
	}
}

func TestSetupRouterUsesForwardedClientIPFromTrustedProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "203.0.113.10/32")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "203.0.113.10:1234", "1.2.3.4", "")
	if got := strings.TrimSpace(response.Body.String()); got != "1.2.3.4" {
		t.Fatalf("ClientIP=%q, want forwarded source", got)
	}
}

func TestSetupRouterIgnoresForwardedHeadersFromUntrustedSource(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "203.0.113.10/32")
	engine := newClientIPTestRouter(t)

	response := serveClientIPRequest(engine, "198.51.100.10:1234", "1.2.3.4", "5.6.7.8")
	if got := strings.TrimSpace(response.Body.String()); got != "198.51.100.10" {
		t.Fatalf("ClientIP=%q, want direct source", got)
	}
}

func TestSetupRouterRejectsInvalidTrustedProxyConfiguration(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "0.0.0.0/0")
	if _, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil); err == nil {
		t.Fatal("SetupRouter unexpectedly accepted a trust-all proxy configuration")
	}
}

func TestSetupRouterCapturesRouteTemplatesAndFiltersNoiseRoutes(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	engine, err := setupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil, nil, provider, true)
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/__otel/:id", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	for _, test := range []struct {
		path       string
		wantStatus int
	}{
		{path: "/api/public/recommendations/posts?limit=17", wantStatus: http.StatusOK},
		{path: "/api/recommendations/posts", wantStatus: http.StatusUnauthorized},
		{path: "/__otel/member-42?token=not-recorded", wantStatus: http.StatusNoContent},
		{path: "/healthz", wantStatus: http.StatusOK},
		{path: "/readyz", wantStatus: http.StatusServiceUnavailable},
		{path: "/metrics", wantStatus: http.StatusOK},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.path == "/__otel/member-42?token=not-recorded" {
			request.Header.Set("Authorization", "Bearer private-test-token")
			request.Header.Set("User-Agent", "private-test-user-agent")
		}
		engine.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Errorf("GET %s status=%d want=%d", test.path, response.Code, test.wantStatus)
		}
	}

	spanNames := make(map[string]bool)
	for _, span := range exporter.GetSpans() {
		spanNames[span.Name] = true
		for _, item := range span.Attributes {
			switch string(item.Key) {
			case "url.path", "client.address", "http.client_ip", "network.peer.address", "network.peer.port", "user_agent.original":
				t.Errorf("sensitive HTTP attribute %q was emitted by span %q", item.Key, span.Name)
			}
			if item.Value.Type() == attribute.STRING && (strings.Contains(item.Value.AsString(), "token=not-recorded") || strings.Contains(item.Value.AsString(), "private-test-token") || strings.Contains(item.Value.AsString(), "private-test-user-agent")) {
				t.Errorf("request detail leaked into span %q", span.Name)
			}
		}
	}
	for _, name := range []string{
		"GET /api/public/recommendations/posts",
		"GET /api/recommendations/posts",
		"GET /__otel/:id",
	} {
		if !spanNames[name] {
			t.Errorf("missing span named %q; got %v", name, spanNames)
		}
	}
	if len(spanNames) != 4 {
		t.Fatalf("spans include unexpected or duplicate names: %v", spanNames)
	}
	if !spanNames["recommendation.response.map"] {
		t.Fatalf("missing response mapping span; got %v", spanNames)
	}
}

func TestHTTPTracingRedactsGinErrorsAndRequestDetails(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	engine, err := setupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil, nil, provider, true)
	if err != nil {
		t.Fatal(err)
	}
	engine.POST("/__otel-error/:id", func(ctx *gin.Context) {
		_ = ctx.Error(errors.New("secret-token=private-value"))
		ctx.Status(http.StatusInternalServerError)
	})
	request := httptest.NewRequest(http.MethodPost, "/__otel-error/member-42?api_key=query-private-value", strings.NewReader("body-private-value"))
	request.Header.Set("Authorization", "Bearer header-private-value")
	request.Header.Set("Cookie", "refresh=private-value")
	request.Header.Set("User-Agent", "agent-private-value")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", response.Code)
	}

	spans := exporter.GetSpans()
	var server *tracetest.SpanStub
	for index := range spans {
		if strings.HasPrefix(spans[index].Name, "POST /__otel-error") {
			server = &spans[index]
			break
		}
	}
	if server == nil {
		t.Fatalf("missing server span: %#v", spans)
	}
	if server.Name != "POST /__otel-error/:id" {
		t.Fatalf("span name=%q, want normalized route", server.Name)
	}
	if server.Parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || server.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("W3C parent not propagated: %v", server.Parent)
	}
	if !hasStringAttribute(server.Attributes, "http.request.method", http.MethodPost) || !hasStringAttribute(server.Attributes, "http.route", "/__otel-error/:id") || !hasIntAttribute(server.Attributes, "http.response.status_code", http.StatusInternalServerError) {
		t.Fatalf("reviewed HTTP attributes missing: %#v", server.Attributes)
	}
	if server.Status.Code != codes.Error || server.Status.Description != "HTTP request failed" {
		t.Fatalf("status=%#v, want sanitized HTTP error", server.Status)
	}
	if len(server.Events) != 0 {
		t.Fatalf("unexpected events may contain raw Gin errors: %#v", server.Events)
	}
	allowed := map[string]bool{
		"http.request.method":       true,
		"http.route":                true,
		"http.response.status_code": true,
	}
	for _, item := range server.Attributes {
		if !allowed[string(item.Key)] {
			t.Errorf("unexpected HTTP attribute %q", item.Key)
		}
	}
	for _, secret := range []string{
		"secret-token=private-value", "query-private-value", "header-private-value",
		"refresh=private-value", "agent-private-value", "body-private-value",
	} {
		if exportedSpanContains(server, secret) {
			t.Errorf("sensitive value %q appeared in exported span", secret)
		}
	}
}

func TestSetupRouterTracingDisabledOmitsHTTPMiddleware(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	engine, err := setupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil, nil, provider, false)
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/__otel-disabled/:id", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/__otel-disabled/member-42", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", response.Code)
	}
	if spans := exporter.GetSpans(); len(spans) != 0 {
		t.Fatalf("disabled tracing emitted spans: %#v", spans)
	}
}

func exportedSpanContains(span *tracetest.SpanStub, value string) bool {
	if strings.Contains(span.Name, value) || strings.Contains(span.Status.Description, value) {
		return true
	}
	for _, item := range span.Attributes {
		if strings.Contains(item.Value.AsString(), value) {
			return true
		}
	}
	for _, event := range span.Events {
		if strings.Contains(event.Name, value) {
			return true
		}
		for _, item := range event.Attributes {
			if strings.Contains(item.Value.AsString(), value) {
				return true
			}
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

func hasIntAttribute(attributes []attribute.KeyValue, key string, want int) bool {
	for _, item := range attributes {
		if string(item.Key) == key && item.Value.AsInt64() == int64(want) {
			return true
		}
	}
	return false
}

func TestSetupRouterTracingPreservesAuthRateLimitAndTimeoutStatuses(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("API_REQUEST_TIMEOUT", "10ms")
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	engine, err := setupRouter(nil, rateLimitRouteVerifier{}, nil, nil, &rateLimitRouteLimiter{}, newRouterRecommendationHandler(t), nil, nil, provider, true)
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/__slow", func(ctx *gin.Context) {
		<-ctx.Request.Context().Done()
	})

	for _, test := range []struct {
		path       string
		authorized bool
		wantStatus int
	}{{path: "/api/recommendations/posts", wantStatus: http.StatusUnauthorized},
		{path: "/api/recommendations/posts", authorized: true, wantStatus: http.StatusTooManyRequests},
		{path: "/__slow", wantStatus: http.StatusGatewayTimeout},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.authorized {
			request.Header.Set("Authorization", "Bearer test-token")
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Errorf("GET %s status=%d want=%d body=%s", test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}

	spanNames := make(map[string]bool)
	for _, span := range exporter.GetSpans() {
		spanNames[span.Name] = true
	}
	for _, name := range []string{
		"GET /api/recommendations/posts",
		"GET /__slow",
	} {
		if !spanNames[name] {
			t.Errorf("missing HTTP span %q; got %v", name, spanNames)
		}
	}
}

func TestSetupRouterRegistersOnlyCanonicalPostMutationRoutes(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"POST /api/auth/logout",
		"GET /api/public/recommendations/posts",
		"POST /api/posts",
		"POST /api/uploads/post-media",
		"POST /api/uploads/profile-avatar",
		"POST /api/uploads/profile-cover",
		"DELETE /api/posts/:id",
		"GET /api/posts/:id/replies",
		"GET /api/posts/:id/quotes",
		"GET /api/me/bookmarks",
		"POST /api/posts/engagement-states",
		"POST /api/posts/bookmark-states",
		"PUT /api/posts/:id/bookmark",
		"DELETE /api/posts/:id/bookmark",
	} {
		if _, ok := routes[route]; !ok {
			t.Fatalf("missing canonical route %q", route)
		}
	}
	for _, route := range []string{
		"POST /api/posts/:id/replies",
		"DELETE /api/posts/:post_id/replies/:reply_id",
		"POST /api/articles",
		"DELETE /api/articles/:id",
		"POST /api/comments",
		"DELETE /api/comments/:id",
		"POST /api/uploads/article-cover",
	} {
		if _, ok := routes[route]; ok {
			t.Fatalf("shadow mutation route is registered: %q", route)
		}
	}
}

func TestSetupRouterKeepsPublicRecommendationsOpenAndRepresentativeAPIsProtected(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}

	publicRequest := httptest.NewRequest(http.MethodGet, "/api/public/recommendations/posts", nil)
	publicResponse := httptest.NewRecorder()
	engine.ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code == http.StatusUnauthorized {
		t.Fatalf("public recommendations unexpectedly require authentication: status=%d body=%s", publicResponse.Code, publicResponse.Body.String())
	}

	protectedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/recommendations/posts"},
		{http.MethodGet, "/api/feed/following"},
		{http.MethodPost, "/api/posts"},
		{http.MethodPost, "/api/posts/engagement-states"},
		{http.MethodPut, "/api/posts/1/like"},
		{http.MethodGet, "/api/users/search?q=guest"},
		{http.MethodGet, "/api/me/notifications"},
	}
	for _, route := range protectedRoutes {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status=%d body=%s, want 401", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestSetupRouterKeepsPublicPostAndProfileReadsOpen(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}

	publicRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/topics"},
		{http.MethodGet, "/api/topics/japan/posts"},
		{http.MethodGet, "/api/posts/not-a-number"},
		{http.MethodGet, "/api/posts/not-a-number/replies"},
		{http.MethodGet, "/api/posts/not-a-number/quotes"},
		{http.MethodGet, "/api/users/not-a-number"},
		{http.MethodGet, "/api/users/not-a-number/timeline"},
	}
	for _, route := range publicRoutes {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code == http.StatusUnauthorized {
			t.Errorf("%s %s unexpectedly requires authentication: body=%s", route.method, route.path, response.Body.String())
		}
	}

	protectedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/users/search?q=guest"},
		{http.MethodGet, "/api/users/1/follow"},
		{http.MethodGet, "/api/users/1/followers"},
		{http.MethodGet, "/api/users/1/following"},
		{http.MethodPost, "/api/posts"},
		{http.MethodPost, "/api/posts/1/translation"},
		{http.MethodPut, "/api/posts/1/like"},
		{http.MethodPut, "/api/posts/1/repost"},
		{http.MethodPut, "/api/posts/1/bookmark"},
		{http.MethodGet, "/api/me/notifications"},
	}
	for _, route := range protectedRoutes {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status=%d body=%s, want 401", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestSetupRouterRegistersProfileTimelineRoute(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	if _, ok := routes["GET /api/users/:id/timeline"]; !ok {
		t.Fatal("missing profile timeline route")
	}
	if _, ok := routes["GET /api/users/:id/posts"]; ok {
		t.Fatal("legacy user posts route is still registered")
	}
}

func TestSetupRouterAllowsIdempotencyKeyForPostCreation(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "/api/posts", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key, Content-Type")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), "idempotency-key") {
		t.Fatalf("allow headers=%q", response.Header().Get("Access-Control-Allow-Headers"))
	}
	normalRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	normalRequest.Header.Set("Origin", "https://app.example.test")
	normalResponse := httptest.NewRecorder()
	engine.ServeHTTP(normalResponse, normalRequest)
	if normalResponse.Code != http.StatusOK {
		t.Fatalf("normal status=%d body=%s", normalResponse.Code, normalResponse.Body.String())
	}
	if !strings.Contains(strings.ToLower(normalResponse.Header().Get("Access-Control-Expose-Headers")), "idempotency-replayed") {
		t.Fatalf("expose headers=%q", normalResponse.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestSetupRouterAllowsGuestRecommendationSessionHeader(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodOptions, "/api/public/recommendations/posts", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "X-Guest-Recommendation-Session")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), "x-guest-recommendation-session") {
		t.Fatalf("allow headers=%q", response.Header().Get("Access-Control-Allow-Headers"))
	}
}

func newClientIPTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine, err := SetupRouter(nil, nil, nil, nil, nil, newRouterRecommendationHandler(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.GET("/__client-ip", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, ctx.ClientIP())
	})
	return engine
}

func serveClientIPRequest(engine *gin.Engine, remoteAddr, forwardedFor, realIP string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/__client-ip", nil)
	request.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if realIP != "" {
		request.Header.Set("X-Real-IP", realIP)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
