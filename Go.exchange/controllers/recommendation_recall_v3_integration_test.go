package controllers

import (
	"math"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

func openRecommendationRecallV3IntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openRecommendationCandidateIntegrationDB(t)
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ArticleEmbedding{}); err != nil {
		t.Fatal(err)
	}

	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "recommendation-recall-v3-test"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	return db
}

func newRecommendationSemanticArticle(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time, vector []float32) models.Article {
	t.Helper()
	article := newRecommendationCandidateIntegrationArticle(t, db, author, title, publishedAt)
	if err := db.Create(&models.ArticleEmbedding{
		ArticleID:   article.ID,
		Version:     config.ActiveEmbeddingVersion(),
		Model:       "recommendation-recall-v3-test",
		Dimensions:  len(vector),
		Embedding:   pgvector.NewVector(vector),
		ContentHash: title,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func cleanupRecommendationRecallV3Data(db *gorm.DB, articleIDs, userIDs []uint) {
	if len(articleIDs) > 0 {
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
	}
	if len(userIDs) > 0 {
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func TestSemanticRecallRecentQuotaAndEvergreenReservationIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-quota-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "semantic-quota-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	articleIDs := make([]uint, 0, 7)
	for index := 0; index < 4; index++ {
		article := newRecommendationSemanticArticle(t, db, author, "semantic-old-"+string(rune('a'+index)), cutoff.Add(-time.Duration(index+1)*24*time.Hour), []float32{1, 0})
		articleIDs = append(articleIDs, article.ID)
	}
	for index := 0; index < 3; index++ {
		article := newRecommendationSemanticArticle(t, db, author, "semantic-recent-"+string(rune('a'+index)), cutoff.Add(time.Duration(index+1)*24*time.Hour), []float32{0.8, 0.6})
		articleIDs = append(articleIDs, article.ID)
	}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

	cfg := defaultRecommendationConfig()
	cfg.SemanticRecall.RecentWindowDays = 30
	cfg.SemanticRecall.RecentRatio = 0.75
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, profile, map[uint]servedArticle{}, now, cfg, false, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("candidate count=%d, want 4; candidates=%#v", len(candidates), candidates)
	}
	recentCount := 0
	evergreenCount := 0
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate.ArticleID]; exists {
			t.Fatalf("duplicate semantic article ID=%d", candidate.ArticleID)
		}
		seen[candidate.ArticleID] = struct{}{}
		var article models.Article
		if err := db.Select("published_at").First(&article, candidate.ArticleID).Error; err != nil {
			t.Fatal(err)
		}
		if article.PublishedAt != nil && !article.PublishedAt.Before(cutoff) {
			recentCount++
		} else {
			evergreenCount++
		}
	}
	if recentCount != 3 || evergreenCount != 1 {
		t.Fatalf("recent=%d evergreen=%d, want 3/1", recentCount, evergreenCount)
	}
	if candidates[3].PositiveSemanticSimilarity < 0.99 {
		t.Fatalf("evergreen reservation selected a weak candidate: %#v", candidates)
	}
}

func TestSemanticRecallPreservesRecommendationEligibilityIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	vector := []float32{1, 0}
	valid := newRecommendationSemanticArticle(t, db, validAuthor, "semantic-valid", now.Add(-time.Hour), vector)
	self := newRecommendationSemanticArticle(t, db, viewer, "semantic-self", now.Add(-2*time.Hour), vector)
	interacted := newRecommendationSemanticArticle(t, db, validAuthor, "semantic-interacted", now.Add(-3*time.Hour), vector)
	notInterested := newRecommendationSemanticArticle(t, db, validAuthor, "semantic-not-interested", now.Add(-4*time.Hour), vector)
	served := newRecommendationSemanticArticle(t, db, validAuthor, "semantic-served", now.Add(-5*time.Hour), vector)
	deleted := newRecommendationSemanticArticle(t, db, deletedAuthor, "semantic-deleted-author", now.Add(-6*time.Hour), vector)
	wrongVersion := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "semantic-wrong-version", now.Add(-7*time.Hour))
	if err := db.Create(&models.ArticleEmbedding{
		ArticleID: wrongVersion.ID, Version: "old-embedding-version", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector(vector), ContentHash: wrongVersion.Title,
	}).Error; err != nil {
		t.Fatal(err)
	}
	articleIDs := []uint{valid.ID, self.ID, interacted.ID, notInterested.ID, served.ID, deleted.ID, wrongVersion.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

	if err := db.Create(&models.ArticleBehavior{
		UserID: viewer.ID, ArticleID: notInterested.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested, Active: true, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	profile := userInterestProfile{
		PositiveVector:       vector,
		InteractedArticleIDs: map[uint]struct{}{interacted.ID: {}},
	}
	servedHistory := map[uint]servedArticle{served.ID: {LastServedAt: now.Add(-time.Minute), Hard: true}}
	cfg := defaultRecommendationConfig()
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, profile, servedHistory, now, cfg, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ArticleID != valid.ID {
		t.Fatalf("candidates=%#v, want only valid article %d", candidates, valid.ID)
	}
}

func TestSemanticRecallUnderfillBackfillsNearestNeighborsWithoutDuplicatesIntegration(t *testing.T) {
	t.Run("recent underfill", func(t *testing.T) {
		db := openRecommendationRecallV3IntegrationDB(t)
		viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-recent-underfill-viewer")
		author := newRecommendationCandidateIntegrationUser(t, db, "semantic-recent-underfill-author")
		now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
		cutoff := now.AddDate(0, 0, -30)
		articleIDs := make([]uint, 0, 5)
		recent := newRecommendationSemanticArticle(t, db, author, "semantic-only-recent", cutoff.Add(time.Hour), []float32{0.7, 0.71414286})
		articleIDs = append(articleIDs, recent.ID)
		for index, vector := range [][]float32{{0.99, 0.14106782}, {0.95, 0.3122499}, {0.9, 0.4358899}, {0.8, 0.6}} {
			article := newRecommendationSemanticArticle(t, db, author, "semantic-underfill-old-"+string(rune('a'+index)), cutoff.Add(-time.Duration(index+1)*24*time.Hour), vector)
			articleIDs = append(articleIDs, article.ID)
		}
		userIDs := []uint{viewer.ID, author.ID}
		t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

		cfg := defaultRecommendationConfig()
		cfg.SemanticRecall.RecentWindowDays = 30
		cfg.SemanticRecall.RecentRatio = 0.75
		candidates, err := loadRecommendationSemanticCandidates(viewer.ID, userInterestProfile{PositiveVector: []float32{1, 0}}, map[uint]servedArticle{}, now, cfg, false, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 4 {
			t.Fatalf("candidate count=%d, want 4; candidates=%#v", len(candidates), candidates)
		}
		assertUniqueRecommendationArticleIDs(t, candidates)
		if candidates[0].ArticleID != recent.ID {
			t.Fatalf("recent candidate=%d, want %d; candidates=%#v", candidates[0].ArticleID, recent.ID, candidates)
		}
	})

	t.Run("evergreen underfill", func(t *testing.T) {
		db := openRecommendationRecallV3IntegrationDB(t)
		viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-evergreen-underfill-viewer")
		author := newRecommendationCandidateIntegrationUser(t, db, "semantic-evergreen-underfill-author")
		now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
		articleIDs := make([]uint, 0, 4)
		for index, vector := range [][]float32{{0.99, 0.14106782}, {0.95, 0.3122499}, {0.9, 0.4358899}, {0.8, 0.6}} {
			article := newRecommendationSemanticArticle(t, db, author, "semantic-recent-only-"+string(rune('a'+index)), now.Add(-time.Duration(index+1)*time.Hour), vector)
			articleIDs = append(articleIDs, article.ID)
		}
		userIDs := []uint{viewer.ID, author.ID}
		t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

		cfg := defaultRecommendationConfig()
		cfg.SemanticRecall.RecentWindowDays = 30
		cfg.SemanticRecall.RecentRatio = 0.75
		candidates, err := loadRecommendationSemanticCandidates(viewer.ID, userInterestProfile{PositiveVector: []float32{1, 0}}, map[uint]servedArticle{}, now, cfg, false, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 4 {
			t.Fatalf("candidate count=%d, want 4; candidates=%#v", len(candidates), candidates)
		}
		assertUniqueRecommendationArticleIDs(t, candidates)
	})
}

func assertUniqueRecommendationArticleIDs(t *testing.T, candidates []embeddingCandidate) {
	t.Helper()
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate.ArticleID]; exists {
			t.Fatalf("duplicate article ID=%d in candidates=%#v", candidate.ArticleID, candidates)
		}
		seen[candidate.ArticleID] = struct{}{}
	}
}

func newRecommendationTrendingArticle(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time, likes, comments int64) models.Article {
	t.Helper()
	article := newRecommendationCandidateIntegrationArticle(t, db, author, title, publishedAt)
	if err := db.Model(&models.Article{}).Where("id = ?", article.ID).Updates(map[string]interface{}{
		"like_count": likes, "comment_count": comments,
	}).Error; err != nil {
		t.Fatal(err)
	}
	article.LikeCount = likes
	article.CommentCount = comments
	return article
}

func TestRecommendationTrendingRecallUsesAgeDecayAndPositiveEngagementIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-order-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "trending-order-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	oldLifetimeWinner := newRecommendationTrendingArticle(t, db, author, "trending-old-lifetime-winner", now.Add(-8*24*time.Hour), 100000, 0)
	olderStrong := newRecommendationTrendingArticle(t, db, author, "trending-older-strong", now.Add(-48*time.Hour), 100, 0)
	newerWeak := newRecommendationTrendingArticle(t, db, author, "trending-newer-weak", now.Add(-time.Hour), 10, 0)
	zeroEngagement := newRecommendationTrendingArticle(t, db, author, "trending-zero-engagement", now.Add(-2*time.Hour), 0, 0)
	articleIDs := []uint{oldLifetimeWinner.ID, olderStrong.ID, newerWeak.ID, zeroEngagement.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

	cfg := defaultRecommendationConfig()
	cfg.Trending.MaxAgeDays = 7
	cfg.Trending.HalfLifeHours = 24
	cfg.Trending.CommentFactor = 0
	candidates, err := loadRecommendationTrendingCandidates(viewer.ID, userInterestProfile{}, map[uint]servedArticle{}, now, cfg, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count=%d, want 2; candidates=%#v", len(candidates), candidates)
	}
	if candidates[0].ArticleID != newerWeak.ID || candidates[1].ArticleID != olderStrong.ID {
		t.Fatalf("candidates=%#v, want newer weak before older strong", candidates)
	}
	for _, candidate := range candidates {
		if !candidate.FromTrending {
			t.Fatalf("candidate metadata=%#v, want FromTrending=true", candidate)
		}
		if candidate.ArticleID == oldLifetimeWinner.ID || candidate.ArticleID == zeroEngagement.ID {
			t.Fatalf("ineligible Trending article returned: %#v", candidate)
		}
	}

	olderRaw := math.Log1p(100) * math.Exp(-math.Ln2*48/cfg.Trending.HalfLifeHours)
	newerRaw := math.Log1p(10) * math.Exp(-math.Ln2*1/cfg.Trending.HalfLifeHours)
	if !(newerRaw > olderRaw) {
		t.Fatalf("test data does not prove decay reorder: newer=%v older=%v", newerRaw, olderRaw)
	}
}

func TestRecommendationTrendingRecallUsesPublishedAtAndIDTieBreakIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-tie-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "trending-tie-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	publishedAt := now.Add(-time.Hour)
	first := newRecommendationTrendingArticle(t, db, author, "trending-tie-first", publishedAt, 20, 4)
	second := newRecommendationTrendingArticle(t, db, author, "trending-tie-second", publishedAt, 20, 4)
	articleIDs := []uint{first.ID, second.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

	cfg := defaultRecommendationConfig()
	candidates, err := loadRecommendationTrendingCandidates(viewer.ID, userInterestProfile{}, map[uint]servedArticle{}, now, cfg, false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ArticleID != second.ID || candidates[1].ArticleID != first.ID {
		t.Fatalf("candidates=%#v, want IDs [%d %d]", candidates, second.ID, first.ID)
	}
}

func TestRecommendationTrendingRecallPreservesEligibilityIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	valid := newRecommendationTrendingArticle(t, db, validAuthor, "trending-valid", now.Add(-time.Hour), 5, 1)
	self := newRecommendationTrendingArticle(t, db, viewer, "trending-self", now.Add(-2*time.Hour), 50, 1)
	notInterested := newRecommendationTrendingArticle(t, db, validAuthor, "trending-not-interested", now.Add(-3*time.Hour), 50, 1)
	interacted := newRecommendationTrendingArticle(t, db, validAuthor, "trending-interacted", now.Add(-4*time.Hour), 50, 1)
	hardServed := newRecommendationTrendingArticle(t, db, validAuthor, "trending-hard-served", now.Add(-5*time.Hour), 50, 1)
	deleted := newRecommendationTrendingArticle(t, db, deletedAuthor, "trending-deleted-author", now.Add(-6*time.Hour), 50, 1)
	nonPublic := newRecommendationTrendingArticle(t, db, validAuthor, "trending-non-public", now.Add(-7*time.Hour), 50, 1)
	articleIDs := []uint{valid.ID, self.ID, notInterested.ID, interacted.ID, hardServed.ID, deleted.ID, nonPublic.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, articleIDs, userIDs) })

	if err := db.Model(&models.ArticleBehavior{}).Create(&models.ArticleBehavior{
		UserID: viewer.ID, ArticleID: notInterested.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested, Active: true, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Article{}).Where("id = ?", nonPublic.ID).Update("publication_state", "draft").Error; err != nil {
		t.Fatal(err)
	}

	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{InteractedArticleIDs: map[uint]struct{}{interacted.ID: {}}}
	served := map[uint]servedArticle{hardServed.ID: {LastServedAt: now.Add(-time.Minute), Hard: true}}
	candidates, err := loadRecommendationTrendingCandidates(viewer.ID, profile, served, now, cfg, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ArticleID != valid.ID {
		t.Fatalf("candidates=%#v, want only valid article %d", candidates, valid.ID)
	}
}
