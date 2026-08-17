package controllers

import (
	"os"
	"testing"
	"time"

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
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	user := models.User{Username: "semantic-owner-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeArticle := func(title string) models.Article {
		article := models.Article{AuthorID: user.ID, Title: title, Content: title + " body", PublicationState: "published", PublishedAt: &now, Model: gorm.Model{CreatedAt: now}}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		return article
	}
	nearest, orthogonal, interacted := makeArticle("nearest"), makeArticle("orthogonal"), makeArticle("interacted")
	for _, item := range []struct {
		article models.Article
		vector  []float32
	}{{nearest, []float32{1, 0}}, {orthogonal, []float32{0, 1}}, {interacted, []float32{1, 0}}} {
		if err := db.Create(&models.ArticleEmbedding{ArticleID: item.article.ID, Version: "post_embedding_v1", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector(item.vector), ContentHash: item.article.Title}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.ArticleBehavior{UserID: user.ID, ArticleID: interacted.ID, Action: ArticleBehaviorActionView, LastSeenAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id IN ?", []uint{nearest.ID, orthogonal.ID, interacted.ID}).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id IN ?", []uint{nearest.ID, orthogonal.ID, interacted.ID}).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	candidates, err := loadSemanticEmbeddingCandidates(user.ID, userInterestProfile{Vector: []float32{1, 0}, InteractedArticleIDs: map[uint]struct{}{interacted.ID: {}}}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 || candidates[0].ArticleID != nearest.ID {
		t.Fatalf("candidates=%#v", candidates)
	}
	for _, candidate := range candidates {
		if candidate.ArticleID == interacted.ID {
			t.Fatal("interacted article was recalled")
		}
	}
	if candidates[0].SemanticSimilarity < .99 {
		t.Fatalf("similarity=%f", candidates[0].SemanticSimilarity)
	}
}
