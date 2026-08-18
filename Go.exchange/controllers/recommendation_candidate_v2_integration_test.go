package controllers

import (
	"errors"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openRecommendationCandidateIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Article{},
		&models.ArticleBehavior{},
		&models.ArticleReaction{},
	); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = nil
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})
	return db
}

func newRecommendationCandidateIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{
		Username: "recommendation-candidate-" + label + "-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func newRecommendationCandidateIntegrationArticle(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Article {
	t.Helper()
	article := models.Article{
		AuthorID:         author.ID,
		Title:            title,
		Content:          "body",
		Preview:          "body",
		PublicationState: consts.ArticlePublicationStatePublished,
		PublishedAt:      &publishedAt,
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func cleanupRecommendationCandidateIntegrationData(db *gorm.DB, articleIDs, userIDs []uint) {
	db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
	db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
}

func TestRecommendationRecallSkipsDeletedAuthorBeforeLimitIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	goodArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "good", now.Add(-time.Minute))
	articleIDs := []uint{badArticle.ID, goodArticle.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, articleIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := loadRecommendationSourceCandidates(
		viewer.ID,
		userInterestProfile{},
		map[uint]servedArticle{},
		now,
		defaultRecommendationConfig(),
		false,
		"articles.published_at DESC, articles.id DESC",
		1,
		"recent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count=%d, want 1", len(candidates))
	}
	if candidates[0].ArticleID != goodArticle.ID {
		t.Fatalf("candidate article ID=%d, want valid article %d", candidates[0].ArticleID, goodArticle.ID)
	}
	if candidates[0].ArticleID == badArticle.ID {
		t.Fatal("deleted-author article was returned")
	}
}

func TestRecommendationHydrationDiscardsDeletedAuthorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "valid", now)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	articleIDs := []uint{validArticle.ID, badArticle.ID}
	userIDs := []uint{validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, articleIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	originalLoader := loadRecommendationArticleEmbeddings
	var embeddingArticleIDs []uint
	loadRecommendationArticleEmbeddings = func(articleIDs []uint, _ string) (map[uint][]float32, error) {
		embeddingArticleIDs = append([]uint(nil), articleIDs...)
		return map[uint][]float32{}, nil
	}
	t.Cleanup(func() {
		loadRecommendationArticleEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{
			{ArticleID: validArticle.ID, FromRecent: true},
			{ArticleID: badArticle.ID, FromPopular: true},
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("hydrated count=%d, want 1", len(hydrated))
	}
	if hydrated[0].Article.ID != validArticle.ID {
		t.Fatalf("hydrated article ID=%d, want %d", hydrated[0].Article.ID, validArticle.ID)
	}
	if hydrated[0].Article.Author.ID != validAuthor.ID || hydrated[0].Article.Author.ID != hydrated[0].Article.AuthorID {
		t.Fatalf("hydrated author=%#v author_id=%d", hydrated[0].Article.Author, hydrated[0].Article.AuthorID)
	}
	if len(embeddingArticleIDs) != 1 || embeddingArticleIDs[0] != validArticle.ID {
		t.Fatalf("embedding article IDs=%v, want [%d]", embeddingArticleIDs, validArticle.ID)
	}
}

func TestRecommendationHydrationAllInvalidAuthorsReturnsEmptyIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	articleIDs := []uint{badArticle.ID}
	userIDs := []uint{deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, articleIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	originalLoader := loadRecommendationArticleEmbeddings
	called := false
	loadRecommendationArticleEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() {
		loadRecommendationArticleEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{{ArticleID: badArticle.ID}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 0 {
		t.Fatalf("hydrated count=%d, want 0", len(hydrated))
	}
	if called {
		t.Fatal("embedding loader was called for all-invalid candidates")
	}
}

func TestRecommendationHydrationPropagatesEmbeddingErrorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "valid", now)
	articleIDs := []uint{validArticle.ID}
	userIDs := []uint{validAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, articleIDs, userIDs)
	})

	sentinel := errors.New("embedding load failure")
	originalLoader := loadRecommendationArticleEmbeddings
	loadRecommendationArticleEmbeddings = func(articleIDs []uint, _ string) (map[uint][]float32, error) {
		if len(articleIDs) != 1 || articleIDs[0] != validArticle.ID {
			t.Fatalf("embedding article IDs=%v, want [%d]", articleIDs, validArticle.ID)
		}
		return nil, sentinel
	}
	t.Cleanup(func() {
		loadRecommendationArticleEmbeddings = originalLoader
	})

	_, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{{ArticleID: validArticle.ID}},
		now,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want sentinel", err)
	}
}
