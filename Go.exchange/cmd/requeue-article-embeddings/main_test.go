package main

import (
	"os"
	"testing"
	"time"

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
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleEmbedding{}, &models.ArticleEmbeddingJob{}); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "embedding-requeue-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var baselineIDs []uint
	if err := db.Table("articles AS a").Select("a.id").Joins("LEFT JOIN article_embeddings AS ae ON ae.article_id = a.id").Where("a.deleted_at IS NULL AND ae.version IS DISTINCT FROM ?", "v2").Pluck("a.id", &baselineIDs).Error; err != nil {
		t.Fatal(err)
	}
	articles := []models.Article{
		{AuthorID: user.ID, Title: "no embedding", Content: "body", PublicationState: "published"},
		{AuthorID: user.ID, Title: "old version", Content: "body", PublicationState: "published"},
		{AuthorID: user.ID, Title: "active version", Content: "body", PublicationState: "published"},
	}
	if err := db.Create(&articles).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleEmbedding{
		ArticleID: articles[1].ID, Version: "v1", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "old",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleEmbedding{
		ArticleID: articles[2].ID, Version: "v2", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	finishedAt := now.Add(-time.Hour)
	if err := db.Create(&models.ArticleEmbeddingJob{
		ArticleID: articles[1].ID, State: models.ArticleEmbeddingJobDead, AttemptCount: 5,
		MaxAttempts: 5, NextAttemptAt: now.Add(time.Hour), LastError: "dead", FinishedAt: &finishedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleEmbeddingJob{
		ArticleID: articles[2].ID, State: models.ArticleEmbeddingJobSucceeded, AttemptCount: 1,
		MaxAttempts: 5, NextAttemptAt: now.Add(time.Hour), FinishedAt: &finishedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{articles[0].ID, articles[1].ID, articles[2].ID}
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleEmbeddingJob{})
		db.Unscoped().Where("article_id IN ?", ids).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	count, err := requeueArticleEmbeddings(db, "v2", now)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(baselineIDs)+2 {
		t.Fatalf("requeued=%d want=%d", count, len(baselineIDs)+2)
	}

	var missingJob, oldJob, activeJob models.ArticleEmbeddingJob
	for _, item := range []struct {
		id  uint
		dst *models.ArticleEmbeddingJob
	}{{articles[0].ID, &missingJob}, {articles[1].ID, &oldJob}, {articles[2].ID, &activeJob}} {
		if err := db.First(item.dst, "article_id = ?", item.id).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []*models.ArticleEmbeddingJob{&missingJob, &oldJob} {
		if job.State != models.ArticleEmbeddingJobQueued || job.AttemptCount != 0 ||
			job.MaxAttempts != requeueArticleEmbeddingMaxAttempts || job.LeasedBy != "" ||
			job.LastError != "" || job.FinishedAt != nil {
			t.Fatalf("requeued job=%#v", job)
		}
		delta := job.NextAttemptAt.Sub(now)
		if delta < -time.Millisecond || delta > time.Millisecond {
			t.Fatalf("requeued next_attempt_at=%v want=%v", job.NextAttemptAt, now)
		}
	}
	if activeJob.State != models.ArticleEmbeddingJobSucceeded || activeJob.AttemptCount != 1 || activeJob.FinishedAt == nil {
		t.Fatalf("active job changed=%#v", activeJob)
	}
}
