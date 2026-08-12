package controllers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestNormalizedRulesV2RecommendationConfigDefaultsWhenMissing(t *testing.T) {
	originalConfig := config.AppConfig
	config.AppConfig = nil
	t.Cleanup(func() { config.AppConfig = originalConfig })

	cfg := normalizedRulesV2RecommendationConfig()

	if cfg.BehaviorWeights.View != 0.5 ||
		cfg.BehaviorWeights.Like != 6 ||
		cfg.BehaviorWeights.Click != 1.5 ||
		cfg.BehaviorWeights.QualifiedRead != 3 ||
		cfg.BehaviorWeights.QuickBounce != 3 ||
		cfg.BehaviorWeights.NotInterested != 6 ||
		cfg.SignalHalfLifeDays != 14 ||
		cfg.FeedbackLookbackDays != 90 ||
		cfg.InterestSaturationScale != 6 ||
		cfg.CategoryWeight != 3 ||
		cfg.TagWeight != 2 ||
		cfg.PopularityWeight != 0.5 ||
		cfg.FreshnessWeight != 1 {
		t.Fatalf("unexpected default recommendation config: %#v", cfg)
	}
}

func TestNormalizedRulesV2RecommendationConfigFallsBackForNonPositiveValues(t *testing.T) {
	withRecommendationConfig(t, config.RecommendationConfig{
		BehaviorWeights: config.RecommendationBehaviorWeights{
			View: 0, Like: -1, Click: 0, QualifiedRead: -1, QuickBounce: 0, NotInterested: -1,
		},
		SignalHalfLifeDays:      -1,
		FeedbackLookbackDays:    0,
		InterestSaturationScale: -1,
		CategoryWeight:          -2,
		TagWeight:               0,
		PopularityWeight:        -0.5,
		FreshnessWeight:         0,
	})

	cfg := normalizedRulesV2RecommendationConfig()

	if cfg.BehaviorWeights.View != 0.5 ||
		cfg.BehaviorWeights.Like != 6 ||
		cfg.BehaviorWeights.Click != 1.5 ||
		cfg.BehaviorWeights.QualifiedRead != 3 ||
		cfg.BehaviorWeights.QuickBounce != 3 ||
		cfg.BehaviorWeights.NotInterested != 6 ||
		cfg.SignalHalfLifeDays != 14 ||
		cfg.FeedbackLookbackDays != 90 ||
		cfg.InterestSaturationScale != 6 ||
		cfg.CategoryWeight != 3 ||
		cfg.TagWeight != 2 ||
		cfg.PopularityWeight != 0.5 ||
		cfg.FreshnessWeight != 1 {
		t.Fatalf("unexpected fallback recommendation config: %#v", cfg)
	}
}

func TestRulesV2ProfileUsesConfiguredBehaviorWeights(t *testing.T) {
	withRecommendationConfig(t, config.RecommendationConfig{
		BehaviorWeights: config.RecommendationBehaviorWeights{View: 10, Like: 1},
	})

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRulesV2RecommendationConfig()
	profile := buildRulesV2InterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, Count: 1, Active: true, LastSeenAt: now},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
		},
		{
			Behavior: models.ArticleBehavior{ArticleID: 2, Action: ArticleBehaviorActionLike, Count: 1, Active: true, LastSeenAt: now},
			Article:  recommendationTestArticle(2, now, "AI", []string{"LLM"}, 0),
		},
	}, nil, now, cfg)

	if profile.Categories["backend"] <= profile.Categories["ai"] {
		t.Fatalf("expected configured view weight to be stronger: profile=%#v", profile.Categories)
	}
	if profile.Tags["go"] <= profile.Tags["llm"] {
		t.Fatalf("expected configured view tag weight to be stronger: profile=%#v", profile.Tags)
	}
}

func TestRulesV2ScoreUsesConfiguredWeights(t *testing.T) {
	withRecommendationConfig(t, config.RecommendationConfig{
		CategoryWeight:   5,
		TagWeight:        7,
		PopularityWeight: 11,
		FreshnessWeight:  13,
	})

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := userInterestProfile{
		Categories:           map[string]float64{"backend": 2},
		Tags:                 map[string]float64{"go": 3},
		InteractedArticleIDs: map[uint]struct{}{},
	}
	article := recommendationTestArticle(1, now.Add(-time.Hour), "Backend", []string{"Go"}, 1)

	got := scoreRulesV2Article(profile, article, now, normalizedRulesV2RecommendationConfig())
	want := 2*float64(5) + 3*float64(7) + math.Log(2)*float64(11) + float64(13)
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("unexpected configured score: got %f want %f", got, want)
	}
}

func TestRulesV2RecommendArticlesPrefersMatchingSignals(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRulesV2RecommendationConfig()
	profile := buildRulesV2InterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 1, Active: true, LastSeenAt: now},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go", "AI"}, 0),
		},
	}, nil, now, cfg)

	recommendations := recommendRulesV2Articles(profile, []models.Article{
		recommendationTestArticle(2, now.Add(-time.Hour), "Travel", []string{"Food"}, 0),
		recommendationTestArticle(3, now.Add(-2*time.Hour), "Backend", []string{"Go"}, 0),
	}, now, cfg, 10)

	if len(recommendations) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(recommendations))
	}
	if recommendations[0].ID != 3 {
		t.Fatalf("expected matching article first, got id=%d score=%f", recommendations[0].ID, recommendations[0].Score)
	}
}

func TestRulesV2ProfileWeightsLikesAboveViews(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRulesV2RecommendationConfig()
	profile := buildRulesV2InterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionView, Count: 1, Active: true, LastSeenAt: now},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
		},
		{
			Behavior: models.ArticleBehavior{ArticleID: 2, Action: ArticleBehaviorActionLike, Count: 1, Active: true, LastSeenAt: now},
			Article:  recommendationTestArticle(2, now, "AI", []string{"LLM"}, 0),
		},
	}, nil, now, cfg)

	if profile.Categories["ai"] <= profile.Categories["backend"] {
		t.Fatalf("expected like-weighted category to be stronger: profile=%#v", profile.Categories)
	}
	if profile.Tags["llm"] <= profile.Tags["go"] {
		t.Fatalf("expected like-weighted tag to be stronger: profile=%#v", profile.Tags)
	}
}

func TestRulesV2ProfileIgnoresInactiveSignals(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	profile := buildRulesV2InterestProfile([]articleBehaviorSignal{
		{
			Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 5, Active: false, LastSeenAt: now},
			Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
		},
	}, nil, now, normalizedRulesV2RecommendationConfig())

	if len(profile.Categories) != 0 || len(profile.Tags) != 0 || len(profile.InteractedArticleIDs) != 0 {
		t.Fatalf("expected inactive behavior to be ignored, got profile=%#v", profile)
	}
}

func TestRulesV2RecommendArticlesFallsBackToPopularityAndFreshnessWithoutBehavior(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRulesV2RecommendationConfig()
	profile := buildRulesV2InterestProfile(nil, nil, now, cfg)

	recommendations := recommendRulesV2Articles(profile, []models.Article{
		recommendationTestArticle(1, now.Add(-60*24*time.Hour), "Old", []string{"x"}, 100),
		recommendationTestArticle(2, now.Add(-24*time.Hour), "Fresh", []string{"y"}, 0),
	}, now, cfg, 10)

	if len(recommendations) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(recommendations))
	}
	if recommendations[0].ID != 1 {
		t.Fatalf("expected popular article first without behavior, got id=%d", recommendations[0].ID)
	}
}

func TestRulesV2RecommendArticlesExcludesInteractedAndClampsLimit(t *testing.T) {
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

	recommendations := recommendRulesV2Articles(profile, candidates, now, normalizedRulesV2RecommendationConfig(), 99)
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
	originalLoadFeedback := loadRecommendationFeedbackSignals
	originalLoadRulesV2Candidates := loadRulesV2Candidates
	defer func() {
		loadRecommendationBehaviorSignals = originalLoadRecommendationBehaviorSignals
		loadRecommendationFeedbackSignals = originalLoadFeedback
		loadRulesV2Candidates = originalLoadRulesV2Candidates
	}()

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	loadRecommendationBehaviorSignals = func(userID uint) ([]articleBehaviorSignal, error) {
		if userID != 11 {
			t.Fatalf("unexpected userID: %d", userID)
		}
		return []articleBehaviorSignal{
			{
				Behavior: models.ArticleBehavior{ArticleID: 1, Action: ArticleBehaviorActionLike, Count: 1, Active: true},
				Article:  recommendationTestArticle(1, now, "Backend", []string{"Go"}, 0),
			},
		}, nil
	}
	loadRecommendationFeedbackSignals = func(uint, time.Time) ([]recommendationFeedbackSignal, error) { return nil, nil }
	loadRulesV2Candidates = func(uint, map[uint]struct{}, time.Time, time.Time) ([]models.Article, error) {
		return []models.Article{
			recommendationTestArticle(2, now.Add(-2*time.Hour), "Travel", []string{"Food"}, 0),
			recommendationTestArticle(3, now.Add(-time.Hour), "Backend", []string{"Go"}, 0),
		}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(11))
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
	if payload[0].Content != "content" || payload[0].Author.DisplayName != "Author Name" || payload[0].Author.AvatarURL != "author.jpg" {
		t.Fatalf("expected canonical content in recommendation payload, got %q", payload[0].Content)
	}
	if payload[0].ID != 3 {
		t.Fatalf("expected highest-scored article first, got id=%d", payload[0].ID)
	}
}

func TestRecommendArticlesIncludesCoverImageURL(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	article := recommendationTestArticle(2, now, "Travel", []string{"Food"}, 0)
	article.CoverImageURL = "/api/files/article-covers/cover.png"
	article.CommentCount = 17
	cfg := normalizedRulesV2RecommendationConfig()
	profile := buildRulesV2InterestProfile(nil, nil, now, cfg)
	expectedScore := scoreRulesV2Article(profile, article, now, cfg)
	recommendations := recommendRulesV2Articles(profile, []models.Article{article}, now, cfg, 1)

	if len(recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recommendations))
	}
	if recommendations[0].CoverImageURL != article.CoverImageURL {
		t.Fatalf("cover image URL=%q", recommendations[0].CoverImageURL)
	}
	if recommendations[0].CommentCount != article.CommentCount || recommendations[0].Score != expectedScore {
		t.Fatalf("comment_count=%d score=%f want comment_count=%d score=%f", recommendations[0].CommentCount, recommendations[0].Score, article.CommentCount, expectedScore)
	}
}

func TestGetArticleRecommendationsSerializesNilTagsAsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalLoadRecommendationBehaviorSignals := loadRecommendationBehaviorSignals
	originalLoadFeedback := loadRecommendationFeedbackSignals
	originalLoadRulesV2Candidates := loadRulesV2Candidates
	t.Cleanup(func() {
		loadRecommendationBehaviorSignals = originalLoadRecommendationBehaviorSignals
		loadRecommendationFeedbackSignals = originalLoadFeedback
		loadRulesV2Candidates = originalLoadRulesV2Candidates
	})

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	loadRecommendationBehaviorSignals = func(uint) ([]articleBehaviorSignal, error) {
		return nil, nil
	}
	loadRecommendationFeedbackSignals = func(uint, time.Time) ([]recommendationFeedbackSignal, error) { return nil, nil }
	loadRulesV2Candidates = func(uint, map[uint]struct{}, time.Time, time.Time) ([]models.Article, error) {
		return []models.Article{
			recommendationTestArticle(2, now, "Travel", nil, 0),
		}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(11))
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/recommendations/articles", nil)

	GetArticleRecommendations(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload []struct {
		Tags json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(payload))
	}
	if got := string(payload[0].Tags); got != "[]" {
		t.Fatalf("expected nil tags to serialize as [], got %s", got)
	}
}

func recommendationTestArticle(id uint, createdAt time.Time, category string, tags []string, likeCount int64) models.Article {
	return models.Article{
		Model:     gorm.Model{ID: id, CreatedAt: createdAt},
		AuthorID:  1,
		Author:    models.User{Model: gorm.Model{ID: 1}, Username: "author", DisplayName: "Author Name", AvatarURL: "author.jpg"},
		Title:     "article",
		Content:   "content",
		Preview:   "preview",
		Summary:   "summary",
		Category:  category,
		Tags:      tags,
		LikeCount: likeCount,
	}
}

func withRecommendationConfig(t *testing.T, recommendation config.RecommendationConfig) {
	t.Helper()
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Recommendation: recommendation}
	t.Cleanup(func() {
		config.AppConfig = originalConfig
	})
}
