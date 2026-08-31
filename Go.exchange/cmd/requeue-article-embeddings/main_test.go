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

func TestRequeuePostEmbeddingsIntegration(t *testing.T) {
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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.PostEmbedding{}); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "embedding-requeue-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	posts := []models.Post{
		{AuthorID: user.ID, Content: "missing", Visibility: "public"},
		{AuthorID: user.ID, Content: "current", Visibility: "public"},
		{AuthorID: user.ID, Content: "stale version", Visibility: "public"},
		{AuthorID: user.ID, Content: "stale content", Visibility: "public"},
		{AuthorID: user.ID, Content: "draft", Visibility: "public"},
		{AuthorID: user.ID, Content: "not published", Visibility: "public"},
		{AuthorID: user.ID, Content: "deleted", Visibility: "public"},
	}
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	currentHash := embeddings.PostEmbeddingContentHash("", "", posts[1].Content)
	embeddingsToCreate := []models.PostEmbedding{
		{PostID: posts[1].ID, Version: "v2", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: currentHash},
		{PostID: posts[2].ID, Version: "v1", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "old"},
		{PostID: posts[3].ID, Version: "v2", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "old"},
	}
	for _, embedding := range embeddingsToCreate {
		if err := db.Create(&embedding).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Delete(&posts[6]).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(posts))
		for _, article := range posts {
			ids = append(ids, article.ID)
		}
		db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	publisher := &reconciliationTestPublisher{}
	stats, err := requeuePostEmbeddings(context.Background(), db, publisher, "v2", now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 6 || stats.Missing != 3 || stats.StaleVersion != 1 || stats.StaleContent != 1 || stats.Published != 5 {
		t.Fatalf("stats=%+v", stats)
	}
	if publisher.calls != 1 || len(publisher.events) != 3 {
		t.Fatalf("publisher calls=%d events=%d", publisher.calls, len(publisher.events))
	}
}
