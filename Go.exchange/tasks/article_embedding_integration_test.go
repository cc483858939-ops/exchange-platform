//go:build legacy_article_embedding_job_tests

package tasks

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type integrationEmbedder struct{ calls int }

func (e *integrationEmbedder) Embed(_ context.Context, _ []string) (embeddings.EmbedResult, error) {
	e.calls++
	return embeddings.EmbedResult{Vectors: [][]float32{{1, 2}}, Model: "integration-model"}, nil
}

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

func TestLegacyArticleEmbeddingLifecycleIntegration(t *testing.T) {
	db := openArticleEmbeddingIntegrationDatabase(t)
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Enabled: true, Model: "configured-model", Version: "post_embedding_v1"}}
	t.Cleanup(func() { global.Db, config.AppConfig = originalDB, originalConfig })

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

	_ = time.Now().UTC()
	embedder := &integrationEmbedder{}
	_ = embedder
	var persisted models.ArticleEmbedding
	if err := db.First(&persisted, "article_id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Dimensions != 2 || len(persisted.Embedding.Slice()) != 2 || persisted.ContentHash == "" {
		t.Fatalf("embedding=%#v", persisted)
	}
}
