package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

func newRecommendationControllerTestContext(path string, viewerID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("user_id", viewerID)
	return ctx, recorder
}

func TestGetPostRecommendationsReturnsPageEnvelopeAndPersistsRequestID(t *testing.T) {
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		persistRecommendationServingTrace = originalPersist
	})

	var persistedRequest models.RecommendationRequest
	recommendationServingPathForHandler = func(userID, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, _ recommendationLanguageContext) (recommendationServingOutcome, error) {
		if userID != 7 || limit != 20 || requestID == "" {
			t.Fatalf("serving args user=%d limit=%d request_id=%q", userID, limit, requestID)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func([]selectedRecommendation) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{
			Post:  postResponse{ID: 101, Media: make([]postMediaResponse, 0)},
			Score: 0.91,
		}}, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	persistRecommendationServingTrace = func(request models.RecommendationRequest, _ []models.RecommendationResultTrace) error {
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
	originalServingPath := recommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	originalTracking := attachRecommendationTrackingForHandler
	originalPersist := persistRecommendationServingTrace
	t.Cleanup(func() {
		recommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
		attachRecommendationTrackingForHandler = originalTracking
		persistRecommendationServingTrace = originalPersist
	})

	var servedLimit uint
	recommendationServingPathForHandler = func(_ uint, limit uint, _ config.RecommendationConfig, _ time.Time, _ string, _ recommendationLanguageContext) (recommendationServingOutcome, error) {
		servedLimit = limit
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func([]selectedRecommendation) ([]recommendedPostResponse, error) {
		return nil, nil
	}
	attachRecommendationTrackingForHandler = func(_ uint, _ string, _ userInterestProfile, _ []selectedRecommendation, _ []recommendedPostResponse, _ time.Time) (int, error) {
		return 0, nil
	}
	persistRecommendationServingTrace = func(models.RecommendationRequest, []models.RecommendationResultTrace) error {
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

func TestParsePublicRecommendationExcludedPostIDsValidatesAndDeduplicates(t *testing.T) {
	got, err := parsePublicRecommendationExcludedPostIDs("1, 2,1,42")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("excluded=%#v", got)
	}
	for _, id := range []uint{1, 2, 42} {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing excluded id %d", id)
		}
	}

	for _, raw := range []string{"1,,2", "0", "-1", "not-a-number"} {
		if _, err := parsePublicRecommendationExcludedPostIDs(raw); err == nil {
			t.Errorf("raw=%q accepted", raw)
		}
	}
	tooMany := strings.TrimSuffix(strings.Repeat("1,", maxPublicRecommendationExcludedPostIDs), ",") + ",2"
	if _, err := parsePublicRecommendationExcludedPostIDs(tooMany); err == nil {
		t.Error("accepted overbound exclusion list")
	}
}

func TestGetPublicPostRecommendationsIsGuestSafeAndUsesPublicEnvelope(t *testing.T) {
	originalServingPath := publicRecommendationServingPathForHandler
	originalResponseBuilder := selectedRecommendationResponsesForHandler
	t.Cleanup(func() {
		publicRecommendationServingPathForHandler = originalServingPath
		selectedRecommendationResponsesForHandler = originalResponseBuilder
	})

	publicRecommendationServingPathForHandler = func(limit uint, _ config.RecommendationConfig, _ time.Time, requestID string, browser recommendationLanguageContext, excluded map[uint]struct{}) (recommendationServingOutcome, error) {
		if limit != 20 || requestID == "" || browser.BrowserPrimary != "en" {
			t.Fatalf("public serving args limit=%d request_id=%q browser=%#v", limit, requestID, browser)
		}
		if len(excluded) != 2 {
			t.Fatalf("excluded=%#v", excluded)
		}
		return recommendationServingOutcome{
			FreshSet: recommendationCandidateSet{Candidates: []embeddingCandidate{{PostID: 101}}},
			Selected: []selectedRecommendation{{}},
		}, nil
	}
	selectedRecommendationResponsesForHandler = func([]selectedRecommendation) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{
			Post:     postResponse{ID: 101, Media: make([]postMediaResponse, 0)},
			Score:    0.5,
			Tracking: &recommendationTrackingResponse{RequestID: "must-be-removed"},
		}}, nil
	}

	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts?exclude_post_ids=1,2", 0)
	ctx.Request.Header.Set("Accept-Language", "en-US,en;q=0.8")
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postRecommendationPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Tracking != nil || !response.Depleted {
		t.Fatalf("response=%#v", response)
	}
}

func TestGetPublicPostRecommendationsRejectsMalformedExclusions(t *testing.T) {
	ctx, recorder := newRecommendationControllerTestContext("/api/public/recommendations/posts?exclude_post_ids=1,,2", 0)
	GetPublicPostRecommendations(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
