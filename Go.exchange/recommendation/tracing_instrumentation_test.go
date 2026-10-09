package recommendation

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/gorm"
)

func TestRecommendationServiceTracingRecordsAuthenticatedStageHierarchy(t *testing.T) {
	exporter, provider := newRecommendationTracingTestProvider(t)
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	candidates := &serviceTestCandidateRepository{
		semantic:  []Candidate{testCandidate(1, CandidateSourceSemantic)},
		following: []Candidate{testCandidate(2, CandidateSourceFollowing)},
		recent:    []Candidate{testCandidate(3, CandidateSourceRecent)},
		trending:  []Candidate{testCandidate(4, CandidateSourceTrending)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 1001},
			2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 1002},
			3: {Model: gorm.Model{ID: 3, CreatedAt: now.Add(-3 * time.Hour)}, AuthorID: 1003},
			4: {Model: gorm.Model{ID: 4, CreatedAt: now.Add(-4 * time.Hour)}, AuthorID: 1004},
		},
	}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: candidates,
			Profiles: &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{
				ProfileStatus: ProfileStatusHit, ProfileAgeMS: 24, PositiveSignalCount: 2,
				PositiveVector: []float32{1, 0},
			}}},
			History: &serviceTestHistoryStore{},
		},
		ServingVersions: serviceTestVersionProvider{version: "embedding-v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	requestID := "business-request-17"
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "GET /api/recommendations/posts")
	result, err := service.Serve(rootCtx, ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: requestID, Now: now,
	})
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != requestID {
		t.Fatalf("business request ID=%q want=%q", result.RequestID, requestID)
	}

	spans := exporter.GetSpans()
	rootSpan := recommendationSpanByName(t, spans, "GET /api/recommendations/posts")
	serviceSpan := recommendationSpanByName(t, spans, "recommendation.service")
	if serviceSpan.Parent.SpanID() != rootSpan.SpanContext.SpanID() {
		t.Fatalf("service span parent=%s HTTP span=%s", serviceSpan.Parent.SpanID(), rootSpan.SpanContext.SpanID())
	}
	if !recommendationSpanHasString(serviceSpan, "recommendation.request_id", requestID) {
		t.Fatalf("business request ID is missing: %#v", serviceSpan.Attributes)
	}
	for _, name := range []string{
		"recommendation.serving_version.load",
		"recommendation.history.load",
		"recommendation.profile.load",
		"recommendation.hydrate",
		"recommendation.author_context.load",
		"recommendation.rank",
		"recommendation.select",
		"recommendation.history.record",
	} {
		if span := recommendationSpanByName(t, spans, name); span.Parent.SpanID() != serviceSpan.SpanContext.SpanID() {
			t.Errorf("%s parent=%s service span=%s", name, span.Parent.SpanID(), serviceSpan.SpanContext.SpanID())
		}
	}
	recall := recommendationSpanByName(t, spans, "recommendation.recall")
	if recall.Parent.SpanID() != serviceSpan.SpanContext.SpanID() || !recommendationSpanHasString(recall, "recommendation.phase", "fresh") {
		t.Fatalf("fresh recall parent or phase is wrong: parent=%s attributes=%#v", recall.Parent.SpanID(), recall.Attributes)
	}
	for _, source := range []string{"semantic", "following", "recent", "trending"} {
		name := "recommendation.recall." + source
		child := recommendationSpanByName(t, spans, name)
		if child.Parent.SpanID() != recall.SpanContext.SpanID() || !recommendationSpanHasString(child, "recommendation.phase", "fresh") {
			t.Errorf("%s parent or phase is wrong: parent=%s attributes=%#v", name, child.Parent.SpanID(), child.Attributes)
		}
	}
	fusion := recommendationSpanByName(t, spans, "recommendation.recall.fusion")
	if fusion.Parent.SpanID() != recall.SpanContext.SpanID() || !recommendationSpanHasString(fusion, "recommendation.source", "fused") {
		t.Errorf("fusion parent or source is wrong: parent=%s attributes=%#v", fusion.Parent.SpanID(), fusion.Attributes)
	}
	for _, span := range spans {
		if span.Name == "recommendation.recall" && recommendationSpanHasString(span, "recommendation.phase", "soft") {
			t.Fatal("unexpected soft recall span when the fresh selection filled the request")
		}
	}
}

func TestRecommendationServiceTracingSeparatesFreshAndSoftRecall(t *testing.T) {
	exporter, provider := newRecommendationTracingTestProvider(t)
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	candidates := &serviceTestCandidateRepository{
		recent:     []Candidate{testCandidate(1, CandidateSourceRecent)},
		softRecent: []Candidate{testCandidate(2, CandidateSourceRecent)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 1001},
			2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 1002},
		},
	}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: candidates,
			Profiles:   &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
			History: &serviceTestHistoryStore{user: ServedHistory{
				2: {PostID: 2, LastServedAt: now.Add(-48 * time.Hour), Soft: true},
			}},
		},
		ServingVersions: serviceTestVersionProvider{version: "embedding-v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "GET /api/recommendations/posts")
	_, err := service.Serve(rootCtx, ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 2, RequestID: uuid.NewString(), Now: now,
	})
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, span := range exporter.GetSpans() {
		if span.Name == "recommendation.recall" {
			if phase, ok := recommendationSpanString(span, "recommendation.phase"); ok {
				phases = append(phases, phase)
			}
		}
	}
	if len(phases) != 2 || phases[0] != "fresh" || phases[1] != "soft" {
		t.Fatalf("recall phases=%v want [fresh soft]", phases)
	}
	if candidates.softQueries == 0 {
		t.Fatal("soft fallback query did not run")
	}
}

func TestRecommendationServiceTracingRecordsGuestFallback(t *testing.T) {
	exporter, provider := newRecommendationTracingTestProvider(t)
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	candidates := &serviceTestCandidateRepository{
		publicRecent: []Candidate{testCandidate(1, CandidateSourceRecent)},
		publicTrend:  []Candidate{testCandidate(2, CandidateSourceTrending)},
		posts: map[uint]models.Post{
			1: {Model: gorm.Model{ID: 1, CreatedAt: now.Add(-time.Hour)}, AuthorID: 1001},
			2: {Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, AuthorID: 1002},
		},
	}
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates},
		ServingVersions:  serviceTestVersionProvider{version: "embedding-v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "GET /api/public/recommendations/posts")
	_, err := service.Serve(rootCtx, ServeRequest{
		Viewer: Viewer{Kind: ViewerGuest}, Limit: 3, RequestID: "guest-request-3", Now: now,
	})
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if candidates.publicCalls != 4 {
		t.Fatalf("public recall calls=%d want=4", candidates.publicCalls)
	}
	for _, source := range []string{"public_recent", "public_trending"} {
		foundFresh, foundFallback := false, false
		for _, span := range spans {
			if span.Name != "recommendation.recall.recent" && span.Name != "recommendation.recall.trending" {
				continue
			}
			if !recommendationSpanHasString(span, "recommendation.source", source) {
				continue
			}
			phase, _ := recommendationSpanString(span, "recommendation.phase")
			foundFresh = foundFresh || phase == "fresh"
			foundFallback = foundFallback || phase == "guest_fallback"
		}
		if !foundFresh || !foundFallback {
			t.Errorf("source %q missing fresh/fallback spans: %#v", source, spans)
		}
	}
	for _, span := range spans {
		if span.Name == "recommendation.recall.semantic" || span.Name == "recommendation.recall.following" || span.Name == "recommendation.profile.load" {
			t.Errorf("guest path emitted a user-only span %q", span.Name)
		}
	}
}

func TestRecommendationServiceTracingMarksErrorsWithoutRecordingDetails(t *testing.T) {
	exporter, provider := newRecommendationTracingTestProvider(t)
	wantErr := errors.New("candidate query failed with private user 42 data")
	service := newServiceTestService(t, ServiceDependencies{
		DataDependencies: DataDependencies{
			Candidates: &serviceTestCandidateRepository{loadErr: wantErr},
			Profiles:   &serviceTestProfileRepository{loaded: ProfileLoadResult{Profile: Profile{ProfileStatus: ProfileStatusMiss}}},
		},
		ServingVersions: serviceTestVersionProvider{version: "embedding-v1"},
	}, ServiceConfig{Recommendation: serviceTestConfig()})
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "GET /api/recommendations/posts")
	_, err := service.Serve(rootCtx, ServeRequest{
		Viewer: Viewer{Kind: ViewerAuthenticated, UserID: 42}, Limit: 1, RequestID: "error-request", Now: time.Now().UTC(),
	})
	root.End()
	if err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("service error=%v want original error=%v", err, wantErr)
	}
	for _, name := range []string{"recommendation.recall.semantic", "recommendation.recall", "recommendation.service"} {
		span := recommendationSpanByName(t, exporter.GetSpans(), name)
		if span.Status.Code != codes.Error {
			t.Errorf("%s status=%v want error", name, span.Status.Code)
		}
		for _, event := range span.Events {
			for _, item := range event.Attributes {
				if item.Value.Type() == attribute.STRING && item.Value.AsString() == wantErr.Error() {
					t.Errorf("%s recorded raw error text", name)
				}
			}
		}
	}
}

func newRecommendationTracingTestProvider(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})
	return exporter, provider
}

func recommendationSpanByName(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %#v", name, spans)
	return tracetest.SpanStub{}
}

func recommendationSpanHasString(span tracetest.SpanStub, key, want string) bool {
	value, ok := recommendationSpanString(span, key)
	return ok && value == want
}

func recommendationSpanString(span tracetest.SpanStub, key string) (string, bool) {
	for _, item := range span.Attributes {
		if string(item.Key) == key && item.Value.Type() == attribute.STRING {
			return item.Value.AsString(), true
		}
	}
	return "", false
}
