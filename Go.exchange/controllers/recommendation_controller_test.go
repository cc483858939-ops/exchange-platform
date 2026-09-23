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

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func ensureRecommendationControllerTestDB(t *testing.T) {
	t.Helper()
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
	recommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, userID, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		if userID != 7 || limit != 20 || requestID == "" {
			t.Fatalf("serving args user=%d limit=%d request_id=%q", userID, limit, requestID)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{
			Post:  postResponse{ID: 101, Media: make([]postMediaResponse, 0)},
			Score: 0.91,
		}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return map[uint]servedPost{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(_ context.Context, request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
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
	recommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, limit uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		servedLimit = limit
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return map[uint]servedPost{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
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
	loadUserRecommendationServedHistoryForHandler = func(_ context.Context, userID uint, _ time.Time, _ config.RecommendationConfig) (map[uint]servedPost, error) {
		if userID != 7 {
			t.Fatalf("loaded user_id=%d", userID)
		}
		events = append(events, "load")
		return map[uint]servedPost{11: {Hard: true}, 12: {Soft: true}}, nil
	}
	recommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
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
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	recordUserRecommendationServedPostsForHandler = func(_ context.Context, userID uint, postIDs []uint, _ time.Time, _ config.RecommendationConfig) error {
		if userID != 7 {
			t.Fatalf("recorded user_id=%d", userID)
		}
		events = append(events, "record")
		recordedPostIDs = append([]uint(nil), postIDs...)
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
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

	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return nil, errors.New("redis unavailable")
	}
	recommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
		if len(served) != 0 {
			t.Fatalf("served=%#v want empty after read failure", served)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
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

	recordUserRecommendationServedPostsForHandler = func(context.Context, uint, []uint, time.Time, config.RecommendationConfig) error {
		return errors.New("redis unavailable")
	}
	recommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{Post: postResponse{ID: 101, Media: make([]postMediaResponse, 0)}}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	tracePersisted := false
	persistRecommendationServingTrace = func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
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
	loadGuestRecommendationServedHistoryForHandler = func(_ context.Context, sessionID string, _ time.Time, _ config.RecommendationConfig) (map[uint]servedPost, error) {
		if sessionID != "4ca3706b-197e-4f63-8f51-f99176f8b61c" {
			t.Fatalf("session_id=%q", sessionID)
		}
		return map[uint]servedPost{1: {Hard: true}, 2: {Soft: true}}, nil
	}
	recordGuestRecommendationServedPostsForHandler = func(_ context.Context, sessionID string, postIDs []uint, _ time.Time, _ config.RecommendationConfig) error {
		recordedSessionID = sessionID
		recordedPostIDs = append([]uint(nil), postIDs...)
		return nil
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, browser recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
		if limit != 20 || requestID == "" || browser.BrowserPrimary != "en" {
			t.Fatalf("public serving args limit=%d request_id=%q browser=%#v", limit, requestID, browser)
		}
		if len(served) != 2 || !served[1].Hard || !served[2].Soft {
			t.Fatalf("served=%#v", served)
		}
		return recommendationServingOutcome{
			FreshSet: recommendationCandidateSet{Candidates: []embeddingCandidate{{PostID: 101}}},
			Selected: []selectedRecommendation{{}},
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
	loadGuestRecommendationServedHistoryForHandler = func(context.Context, string, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		loaderCalls++
		return nil, nil
	}
	recordGuestRecommendationServedPostsForHandler = func(context.Context, string, []uint, time.Time, config.RecommendationConfig) error {
		recorderCalls++
		return nil
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
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
	loadGuestRecommendationServedHistoryForHandler = func(context.Context, string, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return nil, errors.New("redis unavailable")
	}
	recordGuestRecommendationServedPostsForHandler = func(context.Context, string, []uint, time.Time, config.RecommendationConfig) error {
		return errors.New("redis unavailable")
	}
	publicRecommendationServingPathForHandler = func(_ context.Context, _ *gorm.DB, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, served map[uint]servedPost) (recommendationServingOutcome, error) {
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
	var servingDB *gorm.DB
	recommendationServingPathForHandler = func(ctx context.Context, db *gorm.DB, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		servingCtx = ctx
		servingDB = db
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 2*time.Second || time.Until(deadline) > 2500*time.Millisecond {
			t.Fatalf("serving deadline=%v ok=%t", deadline, ok)
		}
		if db == nil || db.Statement == nil || db.Statement.Context != ctx {
			t.Fatalf("serving db context=%#v want %#v", db, ctx)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func(_ *gorm.DB, _ []selectedRecommendation, _ time.Time) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return map[uint]servedPost{}, nil
	}
	recordUserRecommendationServedPostsForHandler = func(context.Context, uint, []uint, time.Time, config.RecommendationConfig) error {
		return nil
	}
	persistRecommendationServingTrace = func(context.Context, models.RecommendationRequest, []models.RecommendationResultTrace) error {
		return nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusOK || servingCtx == nil || servingDB == nil {
		t.Fatalf("status=%d serving_ctx=%v serving_db=%v body=%s", recorder.Code, servingCtx, servingDB, recorder.Body.String())
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
	publicRecommendationServingPathForHandler = func(ctx context.Context, db *gorm.DB, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 2*time.Second || time.Until(deadline) > 2500*time.Millisecond {
			t.Fatalf("public serving deadline=%v ok=%t", deadline, ok)
		}
		if db == nil || db.Statement == nil || db.Statement.Context != ctx {
			t.Fatalf("public serving db context=%#v want %#v", db, ctx)
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
	recommendationServingPathForHandler = func(ctx context.Context, _ *gorm.DB, _ uint, _ uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext, _ map[uint]servedPost) (recommendationServingOutcome, error) {
		servingCalled = true
		cancel()
		servingContextErr = ctx.Err()
		return recommendationServingOutcome{}, servingContextErr
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return map[uint]servedPost{}, nil
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

	recommendationServingPathForHandler = func(context.Context, *gorm.DB, uint, uint, config.RecommendationConfig, time.Time, string, recommendationLanguageContext, map[uint]servedPost) (recommendationServingOutcome, error) {
		return recommendationServingOutcome{}, context.DeadlineExceeded
	}
	loadUserRecommendationServedHistoryForHandler = func(context.Context, uint, time.Time, config.RecommendationConfig) (map[uint]servedPost, error) {
		return map[uint]servedPost{}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/recommendations/posts", 7)
	GetPostRecommendations(ctx)
	if recorder.Code != http.StatusGatewayTimeout || recorder.Body.String() != `{"error":"recommendation timed out"}` {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
