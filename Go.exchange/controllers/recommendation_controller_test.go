package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestRecommendArticlesPrefersMatchingSignals(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := buildUserInterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 1, Active: true},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go", "AI"}, 0),
		},
	})

	recommendations := recommendArticles(profile, []models.Article{
		recommendationTestArticle(2, now.Add(-time.Hour), "Travel", []string{"Food"}, 0),
		recommendationTestArticle(3, now.Add(-2*time.Hour), "Backend", []string{"Go"}, 0),
	}, now, 10)

	if len(recommendations) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(recommendations))
	}
	if recommendations[0].ID != 3 {
		t.Fatalf("expected matching article first, got id=%d score=%f", recommendations[0].ID, recommendations[0].Score)
	}
}

func TestBuildUserInterestProfileWeightsLikesAboveViews(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := buildUserInterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, Count: 1, Active: true},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
		},
		{
			Behavior: models.ArticleBehavior{ArticleID: 2, Action: ArticleBehaviorActionLike, Count: 1, Active: true},
			Article:  recommendationTestArticle(2, now, "AI", []string{"LLM"}, 0),
		},
	})

	if profile.Categories["ai"] <= profile.Categories["backend"] {
		t.Fatalf("expected like-weighted category to be stronger: profile=%#v", profile.Categories)
	}
	if profile.Tags["llm"] <= profile.Tags["go"] {
		t.Fatalf("expected like-weighted tag to be stronger: profile=%#v", profile.Tags)
	}
}

func TestBuildUserInterestProfileIgnoresInactiveSignals(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := buildUserInterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 5, Active: false},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
		},
	})

	if len(profile.Categories) != 0 || len(profile.Tags) != 0 || len(profile.InteractedArticleIDs) != 0 {
		t.Fatalf("expected inactive behavior to be ignored, got profile=%#v", profile)
	}
}

func TestRecommendArticlesFallsBackToPopularityAndFreshnessWithoutBehavior(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := buildUserInterestProfile(nil)

	recommendations := recommendArticles(profile, []models.Article{
		recommendationTestArticle(1, now.Add(-60*24*time.Hour), "Old", []string{"x"}, 100),
		recommendationTestArticle(2, now.Add(-24*time.Hour), "Fresh", []string{"y"}, 0),
	}, now, 10)

	if len(recommendations) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(recommendations))
	}
	if recommendations[0].ID != 1 {
		t.Fatalf("expected popular article first without behavior, got id=%d", recommendations[0].ID)
	}
}

func TestRecommendArticlesExcludesInteractedAndClampsLimit(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := userInterestProfile{
		Categories:           map[string]float64{},
		Tags:                 map[string]float64{},
		InteractedArticleIDs: map[uint]struct{}{2: {}},
	}

	candidates := make([]models.Article, 0, 60)
	for id := uint(1); id <= 60; id++ {
		candidates = append(candidates, recommendationTestArticle(id, now.Add(-time.Duration(id)*time.Minute), "News", []string{"tag"}, int64(id)))
	}

	recommendations := recommendArticles(profile, candidates, now, 99)
	if len(recommendations) != maxRecommendationLimit {
		t.Fatalf("expected clamped result length %d, got %d", maxRecommendationLimit, len(recommendations))
	}
	for _, recommendation := range recommendations {
		if recommendation.ID == 2 {
			t.Fatal("expected interacted article to be excluded")
		}
	}
}

func TestParseRecommendationLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", defaultRecommendationLimit},
		{"0", defaultRecommendationLimit},
		{"bad", defaultRecommendationLimit},
		{"7", 7},
		{"500", maxRecommendationLimit},
	}

	for _, tc := range cases {
		if got := parseRecommendationLimit(tc.raw); got != tc.want {
			t.Fatalf("parseRecommendationLimit(%q)=%d want %d", tc.raw, got, tc.want)
		}
	}
}

func TestGetArticleRecommendationsReturnsSortedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalLoadRecommendationBehaviorSignals := loadRecommendationBehaviorSignals
	originalLoadRecommendationCandidates := loadRecommendationCandidates
	defer func() {
		loadRecommendationBehaviorSignals = originalLoadRecommendationBehaviorSignals
		loadRecommendationCandidates = originalLoadRecommendationCandidates
	}()

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	loadRecommendationBehaviorSignals = func(username string) ([]articleBehaviorSignal, error) {
		if username != "alice" {
			t.Fatalf("unexpected username: %q", username)
		}
		return []articleBehaviorSignal{
			{
				Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 1, Active: true},
				Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
			},
		}, nil
	}
	loadRecommendationCandidates = func(map[uint]struct{}, time.Time) ([]models.Article, error) {
		return []models.Article{
			recommendationTestArticle(2, now.Add(-2*time.Hour), "Travel", []string{"Food"}, 0),
			recommendationTestArticle(3, now.Add(-time.Hour), "Backend", []string{"Go"}, 0),
		}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("username", "alice")
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/recommendations/articles?limit=2", nil)

	GetArticleRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload []recommendedArticleResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(payload))
	}
	if payload[0].ID != 3 {
		t.Fatalf("expected highest-scored article first, got id=%d", payload[0].ID)
	}
}

func recommendationTestArticle(id uint, createdAt time.Time, category string, tags []string, likeCount int64) models.Article {
	return models.Article{
		Model:     gorm.Model{ID: id, CreatedAt: createdAt},
		Title:     "article",
		Preview:   "preview",
		Summary:   "summary",
		Category:  category,
		Tags:      tags,
		LikeCount: likeCount,
	}
}
