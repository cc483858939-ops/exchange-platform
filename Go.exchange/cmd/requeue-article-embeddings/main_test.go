package main

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRequeueArticleEmbeddingsIntegration(t *testing.T) {
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
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleEmbedding{}); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "embedding-requeue-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	publishedAt := now.Add(-time.Minute)
	articles := []models.Article{
		{AuthorID: user.ID, Title: "missing", Content: "body", PublicationState: "published", PublishedAt: &publishedAt},
		{AuthorID: user.ID, Title: "current", Content: "body", PublicationState: "published", PublishedAt: &publishedAt},
		{AuthorID: user.ID, Title: "stale version", Content: "body", PublicationState: "published", PublishedAt: &publishedAt},
		{AuthorID: user.ID, Title: "stale content", Content: "body", PublicationState: "published", PublishedAt: &publishedAt},
		{AuthorID: user.ID, Title: "draft", Content: "body", PublicationState: "draft", PublishedAt: &publishedAt},
		{AuthorID: user.ID, Title: "not published", Content: "body", PublicationState: "published"},
		{AuthorID: user.ID, Title: "deleted", Content: "body", PublicationState: "published", PublishedAt: &publishedAt},
	}
	if err := db.Create(&articles).Error; err != nil {
		t.Fatal(err)
	}
	currentHash := embeddings.ArticleEmbeddingContentHash(articles[1].Title, articles[1].Content)
	embeddingsToCreate := []models.ArticleEmbedding{
		{ArticleID: articles[1].ID, Version: "v2", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: currentHash},
		{ArticleID: articles[2].ID, Version: "v1", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "old"},
		{ArticleID: articles[3].ID, Version: "v2", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "old"},
	}
	for _, embedding := range embeddingsToCreate {
		if err := db.Create(&embedding).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(&articles[6]).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(articles))
		for _, article := range articles {
			ids = append(ids, article.ID)
		}
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	publisher := &reconciliationTestPublisher{}
	stats, err := requeueArticleEmbeddings(context.Background(), db, publisher, "v2", now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 4 || stats.Missing != 1 || stats.StaleVersion != 1 || stats.StaleContent != 1 || stats.Published != 3 {
		t.Fatalf("stats=%+v", stats)
	}
	if publisher.calls != 1 || len(publisher.events) != 3 {
		t.Fatalf("publisher calls=%d events=%d", publisher.calls, len(publisher.events))
	}
}
