package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	recommendationServingPathForHandler = func(userID, limit uint, _ config.RecommendationConfig, _ time.Time, requestID string) (recommendationServingOutcome, error) {
		if userID != 7 || limit != 20 || requestID == "" {
			t.Fatalf("serving args user=%d limit=%d request_id=%q", userID, limit, requestID)
		}
		return recommendationServingOutcome{}, nil
	}
	selectedRecommendationResponsesForHandler = func([]selectedRecommendation) ([]recommendedPostResponse, error) {
		return []recommendedPostResponse{{
			Post:  postResponse{ID: 101},
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
	recommendationServingPathForHandler = func(_ uint, limit uint, _ config.RecommendationConfig, _ time.Time, _ string) (recommendationServingOutcome, error) {
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
