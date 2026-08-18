package controllers

import (
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSemanticEmbeddingRecallUsesExactNearestNeighborAndExclusionsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleEmbedding{}, &models.ArticleBehavior{}, &models.ArticleReaction{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "post_embedding_v1"}}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	viewer := models.User{Username: "semantic-viewer-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "semantic-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeArticle := func(authorID uint, title string) models.Article {
		article := models.Article{AuthorID: authorID, Title: title, Content: title + " body", PublicationState: "published", PublishedAt: &now, Model: gorm.Model{CreatedAt: now}}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		return article
	}
	nearest := makeArticle(author.ID, "nearest")
	orthogonal := makeArticle(author.ID, "orthogonal")
	interacted := makeArticle(author.ID, "interacted")
	selfArticle := makeArticle(viewer.ID, "self")
	for _, item := range []struct {
		article models.Article
		vector  []float32
	}{{nearest, []float32{1, 0}}, {orthogonal, []float32{0, 1}}, {interacted, []float32{1, 0}}, {selfArticle, []float32{1, 0}}} {
		if err := db.Create(&models.ArticleEmbedding{ArticleID: item.article.ID, Version: "post_embedding_v1", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector(item.vector), ContentHash: item.article.Title}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.ArticleBehavior{UserID: viewer.ID, ArticleID: interacted.ID, Action: ArticleBehaviorActionView, LastSeenAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		articleIDs := []uint{nearest.ID, orthogonal.ID, interacted.ID, selfArticle.ID}
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", []uint{viewer.ID, author.ID}).Delete(&models.User{})
	})

	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}, InteractedArticleIDs: map[uint]struct{}{interacted.ID: {}}}
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, profile, map[uint]servedArticle{}, now, cfg, false, cfg.Candidates.Personalized.Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates=%#v, want exactly 2 eligible semantic candidates", candidates)
	}
	if candidates[0].ArticleID != nearest.ID {
		t.Fatalf("first candidate=%d similarity=%f, want nearest=%d; candidates=%#v", candidates[0].ArticleID, candidates[0].PositiveSemanticSimilarity, nearest.ID, candidates)
	}
	if candidates[1].ArticleID != orthogonal.ID {
		t.Fatalf("second candidate=%d, want orthogonal=%d; candidates=%#v", candidates[1].ArticleID, orthogonal.ID, candidates)
	}
	for _, candidate := range candidates {
		if candidate.ArticleID == interacted.ID {
			t.Fatal("interacted article was recalled")
		}
		if candidate.ArticleID == selfArticle.ID {
			t.Fatal("self-authored article was recalled")
		}
	}
	if candidates[0].PositiveSemanticSimilarity < .99 {
		t.Fatalf("similarity=%f", candidates[0].PositiveSemanticSimilarity)
	}
}

func TestSemanticEmbeddingRecallFiltersActiveVersionIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleEmbedding{}, &models.ArticleBehavior{}, &models.ArticleReaction{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "v2"}}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	viewer := models.User{Username: "semantic-version-viewer-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "semantic-version-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldArticle := models.Article{AuthorID: author.ID, Title: "old", Content: "old", PublicationState: "published", PublishedAt: &now}
	activeArticle := models.Article{AuthorID: author.ID, Title: "active", Content: "active", PublicationState: "published", PublishedAt: &now}
	if err := db.Create(&oldArticle).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&activeArticle).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		articleID uint
		version   string
	}{
		{oldArticle.ID, "v1"},
		{activeArticle.ID, "v2"},
	} {
		if err := db.Create(&models.ArticleEmbedding{
			ArticleID: item.articleID, Version: item.version, Model: "test", Dimensions: 2,
			Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: item.version,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ids := []uint{oldArticle.ID, activeArticle.ID}
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", []uint{viewer.ID, author.ID}).Delete(&models.User{})
	})

	cfg := defaultRecommendationConfig()
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, userInterestProfile{
		PositiveVector: []float32{1, 0},
	}, map[uint]servedArticle{}, now, cfg, false, cfg.Candidates.Personalized.Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ArticleID != activeArticle.ID {
		t.Fatalf("candidates=%#v", candidates)
	}
}
