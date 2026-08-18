package tasks

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openArticleEmbeddingIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
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
	return db
}

func TestArticleEmbeddingGORMStoreIntegration(t *testing.T) {
	db := openArticleEmbeddingIntegrationDatabase(t)

	user := models.User{Username: "embedding-owner-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Article{AuthorID: user.ID, Title: "Title", Content: "Body", PublicationState: "published"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id = ?", article.ID).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	store := gormArticleEmbeddingStore{db: db}
	loadedArticle, err := store.GetArticle(context.Background(), article.ID)
	if err != nil || loadedArticle.ID != article.ID {
		t.Fatalf("article=%#v err=%v", loadedArticle, err)
	}
	if _, err := store.GetEmbedding(context.Background(), article.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing embedding err=%v", err)
	}

	first := models.ArticleEmbedding{
		ArticleID: article.ID, Version: "v1", Model: "test-model",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 2}),
		ContentHash: "hash-v1", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.UpsertEmbedding(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Version = "v2"
	second.ContentHash = "hash-v2"
	second.Embedding = pgvector.NewVector([]float32{3, 4})
	second.UpdatedAt = time.Now().UTC()
	if err := store.UpsertEmbedding(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetEmbedding(context.Background(), article.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != "v2" || persisted.ContentHash != "hash-v2" || len(persisted.Embedding.Slice()) != 2 {
		t.Fatalf("embedding=%#v", persisted)
	}
	var count int64
	if err := db.Model(&models.ArticleEmbedding{}).Where("article_id = ?", article.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows=%d want=1", count)
	}
}
