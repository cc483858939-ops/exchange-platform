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
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleEmbedding{}, &models.ArticleEmbeddingJob{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestArticleEmbeddingJobLifecycleIntegration(t *testing.T) {
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
	job := models.ArticleEmbeddingJob{ArticleID: article.ID, State: models.ArticleEmbeddingJobQueued, MaxAttempts: 5, NextAttemptAt: time.Unix(0, 0).UTC()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id = ?", article.ID).Delete(&models.ArticleEmbeddingJob{})
		db.Unscoped().Where("article_id = ?", article.ID).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	claimed, err := claimArticleEmbeddingJob(db, time.Now().UTC(), "integration-worker", time.Minute)
	if err != nil || claimed == nil || claimed.State != models.ArticleEmbeddingJobLeased || claimed.AttemptCount != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	embedder := &integrationEmbedder{}
	if err := processArticleEmbeddingJob(context.Background(), *claimed, embedder); err != nil {
		t.Fatal(err)
	}
	var persisted models.ArticleEmbedding
	if err := db.First(&persisted, "article_id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Dimensions != 2 || len(persisted.Embedding.Slice()) != 2 || persisted.ContentHash == "" {
		t.Fatalf("embedding=%#v", persisted)
	}
	var completed models.ArticleEmbeddingJob
	if err := db.First(&completed, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.State != models.ArticleEmbeddingJobSucceeded || embedder.calls != 1 {
		t.Fatalf("job=%#v calls=%d", completed, embedder.calls)
	}

	if err := db.Model(&models.ArticleEmbeddingJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{"state": models.ArticleEmbeddingJobQueued, "attempt_count": 0, "next_attempt_at": time.Unix(0, 0).UTC(), "leased_by": "", "lease_until": nil, "finished_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	config.AppConfig.Embedding.Version = "post_embedding_v2"
	claimed, err = claimArticleEmbeddingJob(db, time.Now().UTC(), "integration-worker-v2", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("version-change claimed=%#v err=%v", claimed, err)
	}
	if err := processArticleEmbeddingJob(context.Background(), *claimed, embedder); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 2 {
		t.Fatalf("version change calls=%d want=2", embedder.calls)
	}
	if err := db.First(&persisted, "article_id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Version != "post_embedding_v2" {
		t.Fatalf("persisted version=%q", persisted.Version)
	}

	if err := db.Model(&models.ArticleEmbeddingJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{"state": models.ArticleEmbeddingJobQueued, "attempt_count": 0, "next_attempt_at": time.Unix(0, 0).UTC(), "leased_by": "", "lease_until": nil, "finished_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = claimArticleEmbeddingJob(db, time.Now().UTC(), "integration-worker-v2-same", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("same-version claimed=%#v err=%v", claimed, err)
	}
	if err := processArticleEmbeddingJob(context.Background(), *claimed, embedder); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 2 {
		t.Fatalf("same-version skip calls=%d want=2", embedder.calls)
	}
}
