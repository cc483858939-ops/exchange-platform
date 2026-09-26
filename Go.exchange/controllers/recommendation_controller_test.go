package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func ensureRecommendationControllerTestDB(t *testing.T) {
	t.Helper()
	originalServingVersionLoader := loadRecommendationServingVersionForHandler
	loadRecommendationServingVersionForHandler = func(context.Context, *gorm.DB) (string, error) {
		return "post_embedding_v1", nil
	}
	t.Cleanup(func() { loadRecommendationServingVersionForHandler = originalServingVersionLoader })
	if global.Db != nil {
		return
	}
	db, err := gorm.Open(postgres.Open("postgres://unused.invalid/unused"), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	global.Db = db
	t.Cleanup(func() { global.Db = nil })
}

func newRecommendationControllerTestContext(path string, viewerID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("user_id", viewerID)
	return ctx, recorder
}

func TestGetPostRecommendationsReturnsPageEnvelopeAndPersistsRequestID(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	var persistedRequest models.RecommendationRequest
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, userID, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, snapshot recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		if userID != 7 || limit != 20 || requestID == "" {
			t.Fatalf("serving args user=%d limit=%d request_id=%q", userID, limit, requestID)
		}
		return recommendationServingOutcome{EmbeddingVersion: snapshot.EmbeddingVersion}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{
			Post:  postResponse{ID: 101, Media: make([]postMediaResponse, 0)},
			Score: 0.91,
		}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(_ context.Context, _ recommendation.TraceRepository, request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		persistedRequest = request
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts?limit=20", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response postRecommendationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Post.ID != 101 || response.Items[0].Score != 0.91 || response.RequestID == "" || response.Depleted {
		t.Fatalf("response=%#v", response)
	}
	if persistedRequest.RequestID == "" || persistedRequest.RequestID != response.RequestID {
		t.Fatalf("persisted request_id=%q response request_id=%q", persistedRequest.RequestID, response.RequestID)
	}
}

func TestGetPostRecommendationsUsesOneServingSnapshotForTrackingAndTrace(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponses := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalLoader := loadRecommendationServingVersionForHandler
	originalHistory := loadUserRecommendationServedHistoryForHandler
	originalRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponses
		attachRecommendationTrackingForHandler = originalTracking
		loadRecommendationServingVersionForHandler = originalLoader
		loadUserRecommendationServedHistoryForHandler = originalHistory
		recordUserRecommendationServedPostsForHandler = originalRecorder
		persistRecommendationServingTrace = originalPersist
	})

	currentServingVersion := "post_embedding_v1"
	loaderCalls := 0
	var servingVersions []string
	var trackingVersions []string
	var persistedHashes []string
	loadRecommendationServingVersionForHandler = func(context.Context, *gorm.DB) (string, error) {
		loaderCalls++
		return currentServingVersion, nil
	}
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, snapshot recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		servingVersions = append(servingVersions, snapshot.EmbeddingVersion)
		currentServingVersion = "post_embedding_v2"
		return recommendationServingOutcome{
			EmbeddingVersion: snapshot.EmbeddingVersion,
			Profile:          userInterestProfile{ProfileStatus: recommendationProfileStatusMiss},
			Selected:         []selectedRecommendation{{Post: models.Post{Model: gorm.Model{ID: 101}}}},
		}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, servingVersion string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		trackingVersions = append(trackingVersions, servingVersion)
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(_ context.Context, _ recommendation.TraceRepository, request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
		persistedHashes = append(persistedHashes, request.RankerConfigHash)
		return nil
	}

	for range 2 {
		ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
		GetPostRecommendations(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if loaderCalls != 2 || len(servingVersions) != 2 || len(trackingVersions) != 2 || len(persistedHashes) != 2 {
		t.Fatalf("loader calls=%d serving=%v tracking=%v hashes=%v", loaderCalls, servingVersions, trackingVersions, persistedHashes)
	}
	if servingVersions[0] != "post_embedding_v1" || servingVersions[1] != "post_embedding_v2" ||
		trackingVersions[0] != servingVersions[0] || trackingVersions[1] != servingVersions[1] {
		t.Fatalf("serving snapshots=%v tracking snapshots=%v", servingVersions, trackingVersions)
	}
	cfg := normalizedRecommendationConfig()
	if persistedHashes[0] != recommendationRankerConfigHash(cfg, servingVersions[0]) || persistedHashes[1] != recommendationRankerConfigHash(cfg, servingVersions[1]) {
		t.Fatalf("persisted hashes=%v do not match request snapshots=%v", persistedHashes, servingVersions)
	}
}

func TestGetPostRecommendationsReturnsEmptyPageAsEmptyArrayAndDepleted(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	var servedLimit uint
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, limit uint, _ config.RecommendationConfig, _ time.Time, _ string, snapshot recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		servedLimit = limit
		return recommendationServingOutcome{EmbeddingVersion: snapshot.EmbeddingVersion}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, recommendation.TraceRepository, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts?limit=not-a-number", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || servedLimit != defaultRecommendationLimit {
		t.Fatalf("status=%d limit=%d body=%s", recorder.Code, servedLimit, recorder.Body.String())
	}
	var response postRecommendationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Items == nil || len(response.Items) != 0 || response.RequestID == "" || !response.Depleted {
		t.Fatalf("response=%#v body=%s", response, recorder.Body.String())
	}
}

func TestGetPostRecommendationsLoadsAndRecordsUserServedHistory(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	events := make([]string, 0, 4)
	var recordedPostIDs []uint
	loadUserRecommendationServedHistoryForHandler = func(_ context.Context, _ recommendation.HistoryStore, userID uint, _ time.Time, _ config.RecommendationConfig) (recommendation.ServedHistory, error) {
		if userID != 7 {
			t.Fatalf("loaded user_id=%d", userID)
		}
		events = append(events, "load")
		return recommendation.ServedHistory{11: {Hard: true}, 12: {Soft: true}}, nil
	}
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
		events = append(events, "serve")
		if len(served) != 2 || !served[11].Hard || !served[12].Soft {
			t.Fatalf("served=%#v", served)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{
			{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}},
			{Post: postResponse{ID: 102, Media: make([]postMediaResponse, 0)}},
			{Post: postResponse{ID: 0, Media: make([]postMediaResponse, 0)}},
		}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	recordUserRecommendationServedPostsForHandler = func(_ context.Context, _ recommendation.HistoryStore, userID uint, postIDs []uint, _ time.Time, _ config.RecommendationConfig) error {
		if userID != 7 {
			t.Fatalf("recorded user_id=%d", userID)
		}
		events = append(events, "record")
		recordedPostIDs = append([]uint(nil), postIDs...)
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, recommendation.TraceRepository, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		events = append(events, "trace")
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(recordedPostIDs) != 2 || recordedPostIDs[0] != 101 || recordedPostIDs[1] != 102 {
		t.Fatalf("recorded post IDs=%v want [101 102]", recordedPostIDs)
	}
	if len(events) != 4 || events[0] != "load" || events[1] != "serve" || events[2] != "record" || events[3] != "trace" {
		t.Fatalf("events=%v want [load serve record trace]", events)
	}
}

func TestGetPostRecommendationsUserHistoryReadFailureFailsOpen(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return nil, errors.New("redis unavailable")
	}
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
		if len(served) != 0 {
			t.Fatalf("served=%#v want empty after read failure", served)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, recommendation.TraceRepository, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostRecommendationsUserHistoryWriteFailureStillPersistsTrace(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return errors.New("redis unavailable")
	}
	recommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	tracePersisted := false
	persistRecommendationServingTrace = func(context.Context, recommendation.TraceRepository, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		tracePersisted = true
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || !tracePersisted {
		t.Fatalf("status=%d trace_persisted=%t body=%s", recorder.Code, tracePersisted, recorder.Body.String())
	}
}

func TestParseRecommendationLimitPreservesDefaultsAndMaximum(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int
	}{
		{raw: "", want: defaultRecommendationLimit},
		{raw: "not-a-number", want: defaultRecommendationLimit},
		{raw: "0", want: defaultRecommendationLimit},
		{raw: "20", want: 20},
		{raw: "100", want: maxRecommendationLimit},
	} {
		if got := parseRecommendationLimit(test.raw); got != test.want {
			t.Errorf("raw=%q got=%d want=%d", test.raw, got, test.want)
		}
	}
}

func TestParseGuestRecommendationSessionIDCanonicalizesAndRejectsInvalidValues(t *testing.T) {
	got, ok := parseGuestRecommendationSessionID("  4CA3706B-197E-4F63-8F51-F99176F8B61C  ")
	if !ok || got != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
		t.Fatalf("session=%q valid=%t", got, ok)
	}
	for _, raw := range []string{"", "   ", "not-a-uuid", "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000x"} {
		if got, ok := parseGuestRecommendationSessionID(raw); ok || got != "" {
			t.Fatalf("raw=%q parsed=%q valid=%t", raw, got, ok)
		}
	}
}

func TestGetPublicPostRecommendationsIsGuestSafeAndUsesPublicEnvelope(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalLoader := loadGuestRecommendationServedHistoryForHandler
	originalRecorder := recordGuestRecommendationServedPostsForHandler
	t.Cleanup(func() {
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		loadGuestRecommendationServedHistoryForHandler = originalLoader
		recordGuestRecommendationServedPostsForHandler = originalRecorder
	})

	const rawSessionID = "4CA3706B-197E-4F63-8F51-F99176F8B61C"
	var recordedSessionID string
	var recordedPostIDs []uint
	loadGuestRecommendationServedHistoryForHandler = func(_ context.Context, _ recommendation.HistoryStore, sessionID string, _ time.Time, _ config.RecommendationConfig) (recommendation.ServedHistory, error) {
		if sessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
			t.Fatalf("session_id=%q", sessionID)
		}
		return recommendation.ServedHistory{1: {Hard: true}, 2: {Soft: true}}, nil
	}
	recordGuestRecommendationServedPostsForHandler = func(_ context.Context, _ recommendation.HistoryStore, sessionID string, postIDs []uint, _ time.Time, _ config.RecommendationConfig) error {
		recordedSessionID = sessionID
		recordedPostIDs = append([]uint(nil), postIDs...)
		return nil
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, _ recommendationServingSnapshot, browser recommendationLanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
		if limit != 20 || requestID == "" || browser.BrowserPrimary != "en" {
			t.Fatalf("public serving args limit=%d request_id=%q browser=%#v", limit, requestID, browser)
		}
		if len(served) != 2 || !served[1].Hard || !served[2].Soft {
			t.Fatalf("served=%#v", served)
		}
		return recommendationServingOutcome{
			EmbeddingVersion: "post_embedding_v1",
			FreshSet:         recommendationCandidateSet{Candidates: []embeddingCandidate{{PostID: 101}}},
			Selected:         []selectedRecommendation{{}},
		}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{
			{
				Post:     postResponse{ID: 101, Media: make([]postMediaResponse, 0)},
				Score:    0.5,
				Tracking: &recommendationTrackingResponse{RequestID: "must-be-removed"},
			},
			{
				Post:     postResponse{ID: 102, Media: make([]postMediaResponse, 0)},
				Score:    0.4,
				Tracking: &recommendationTrackingResponse{RequestID: "must-be-removed"},
			},
		}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts?limit=20", 0)
	ctx.Request.Header.Set("Accept-Language", "en-US,en;q=0.8")
	ctx.Request.Header.Set(guestRecommendationSessionHeader, rawSessionID)
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postRecommendationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Tracking != nil || response.Items[1].Tracking != nil || !response.Depleted {
		t.Fatalf("response=%#v", response)
	}
	if recordedSessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" || len(recordedPostIDs) != 2 || recordedPostIDs[0] != 101 || recordedPostIDs[1] != 102 {
		t.Fatalf("recorded session=%q post_ids=%v", recordedSessionID, recordedPostIDs)
	}
}

func TestGetPublicPostRecommendationsLoadsServingSnapshotOnce(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalLoader := loadRecommendationServingVersionForHandler
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponses := selectedRecommendationResponsesForHandler
	t.Cleanup(func() {
		loadRecommendationServingVersionForHandler = originalLoader
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponses
	})
	loaderCalls := 0
	var servingVersion string
	loadRecommendationServingVersionForHandler = func(context.Context, *gorm.DB) (string, error) {
		loaderCalls++
		return "post_embedding_v2", nil
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, snapshot recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		servingVersion = snapshot.EmbeddingVersion
		return recommendationServingOutcome{EmbeddingVersion: snapshot.EmbeddingVersion}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts", 0)
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || loaderCalls != 1 || servingVersion != "post_embedding_v2" {
		t.Fatalf("status=%d loader calls=%d serving version=%q body=%s", recorder.Code, loaderCalls, servingVersion, recorder.Body.String())
	}
}

func TestGetPublicPostRecommendationsInvalidOrMissingGuestSessionFailsOpen(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalLoader := loadGuestRecommendationServedHistoryForHandler
	originalRecorder := recordGuestRecommendationServedPostsForHandler
	t.Cleanup(func() {
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		loadGuestRecommendationServedHistoryForHandler = originalLoader
		recordGuestRecommendationServedPostsForHandler = originalRecorder
	})
	loaderCalls := 0
	recorderCalls := 0
	loadGuestRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, string, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		loaderCalls++
		return nil, nil
	}
	recordGuestRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, string, []uint, time.Time, config.RecommendationConfig) error {
		recorderCalls++
		return nil
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
		if len(served) != 0 {
			t.Fatalf("served=%#v", served)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}

	for _, raw := range []string{"", "not-a-uuid"} {
		ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts", 0)
		if raw != "" {
			ctx.Request.Header.Set(guestRecommendationSessionHeader, raw)
		}
		GetPublicPostRecommendations(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("raw=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
	if loaderCalls != 0 || recorderCalls != 0 {
		t.Fatalf("invalid/missing guest session performed history I/O: loads=%d records=%d", loaderCalls, recorderCalls)
	}
}

func TestGetPublicPostRecommendationsGuestHistoryFailuresFailOpen(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalLoader := loadGuestRecommendationServedHistoryForHandler
	originalRecorder := recordGuestRecommendationServedPostsForHandler
	t.Cleanup(func() {
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		loadGuestRecommendationServedHistoryForHandler = originalLoader
		recordGuestRecommendationServedPostsForHandler = originalRecorder
	})
	loadGuestRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, string, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return nil, errors.New("redis unavailable")
	}
	recordGuestRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, string, []uint, time.Time, config.RecommendationConfig) error {
		return errors.New("redis unavailable")
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ recommendation.DataDependencies, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, served recommendation.ServedHistory) (recommendationServingOutcome, error) {
		if len(served) != 0 {
			t.Fatalf("served=%#v", served)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts", 0)
	ctx.Request.Header.Set(guestRecommendationSessionHeader, "4ca3706b-197e-4f63-8f51-f99176f8b61c")
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostRecommendationsPassesDeadlineAndScopedDB(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalConfig := config.AppConfig
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	originalUserRecorder := recordUserRecommendationServedPostsForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		config.AppConfig = originalConfig
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
		recordUserRecommendationServedPostsForHandler = originalUserRecorder
		persistRecommendationServingTrace = originalPersist
	})

	config.AppConfig = &config.Config{Recommendation: config.RecommendationConfig{ServingTimeoutMS: 2500}}
	var servingCtx context.Context
	var servingDependencies recommendation.DataDependencies
	recommendationServingPathForHandler = func(ctx context.Context, dependencies recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		servingCtx = ctx
		servingDependencies = dependencies
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 2*time.Second || time.Until(deadline) > 2500*time.Millisecond {
			t.Fatalf("serving deadline=%v ok=%t", deadline, ok)
		}
		if dependencies.Candidates == nil || dependencies.Profiles == nil || dependencies.Traces == nil {
			t.Fatalf("serving repositories were not wired: %#v", dependencies)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, recommendation.HistoryStore, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, recommendation.TraceRepository, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || servingCtx == nil || servingDependencies.Candidates == nil {
		t.Fatalf("status=%d serving_ctx=%v serving_dependencies=%#v body=%s", recorder.Code, servingCtx, servingDependencies, recorder.Body.String())
	}
}

func TestGetPublicPostRecommendationsPassesDeadlineAndScopedDB(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalConfig := config.AppConfig
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	t.Cleanup(func() {
		config.AppConfig = originalConfig
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
	})

	config.AppConfig = &config.Config{Recommendation: config.RecommendationConfig{ServingTimeoutMS: 2500}}
	publicRecommendationServingPathForHandler = func(ctx context.Context, dependencies recommendation.DataDependencies, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 2*time.Second || time.Until(deadline) > 2500*time.Millisecond {
			t.Fatalf("public serving deadline=%v ok=%t", deadline, ok)
		}
		if dependencies.Candidates == nil {
			t.Fatalf("public candidate repository was not wired: %#v", dependencies)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts", 0)
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostRecommendationsPropagatesParentCancellationWithoutWritingJSON(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
	})

	servingCalled := false
	var servingContextErr error
	parent, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	recommendationServingPathForHandler = func(ctx context.Context, _ recommendation.DataDependencies, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationServingSnapshot, _ recommendationLanguageContext, _ recommendation.ServedHistory) (recommendationServingOutcome, error) {
		servingCalled = true
		cancel()
		servingContextErr = ctx.Err()
		return recommendationServingOutcome{}, servingContextErr
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	ctx.Request = ctx.Request.WithContext(parent)
	GetPostRecommendations(ctx)
	if !servingCalled || !errors.Is(servingContextErr, context.Canceled) {
		t.Fatalf("serving_called=%t serving context error=%v, want context.Canceled", servingCalled, servingContextErr)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote response body=%s", recorder.Body.String())
	}
}

func TestGetPostRecommendationsServingDeadlineReturnsGatewayTimeout(t *testing.T) {
	ensureRecommendationControllerTestDB(t)
	originalServingPath := recommendationServingPathForHandler
	originalUserLoader := loadUserRecommendationServedHistoryForHandler
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		loadUserRecommendationServedHistoryForHandler = originalUserLoader
	})

	recommendationServingPathForHandler = func(context.Context, recommendation.DataDependencies, uint, uint, config.RecommendationConfig, time.Time, string, recommendationServingSnapshot, recommendationLanguageContext, recommendation.ServedHistory) (recommendationServingOutcome, error) {
		return recommendationServingOutcome{}, context.DeadlineExceeded
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, recommendation.HistoryStore, uint, time.Time, config.RecommendationConfig) (recommendation.ServedHistory, error) {
		return recommendation.ServedHistory{}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusGatewayTimeout || recorder.Body.String() != `{"error":"recommendation timed out"}` {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
