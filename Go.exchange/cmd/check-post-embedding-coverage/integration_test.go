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

func TestPostEmbeddingCoverageCountsOnlyEligibleRootsIntegration(t *testing.T) {
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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostEmbedding{}); err != nil {
		t.Fatal(err)
	}

	activeAuthor := models.User{Username: "embedding-coverage-active-" + uuid.NewString(), Password: "test"}
	deletedAuthor := models.User{Username: "embedding-coverage-deleted-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&activeAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	posts := []models.Post{
		{AuthorID: activeAuthor.ID, Content: "ready one", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "ready two", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "v1 only", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "deleted post", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "reply", Visibility: "public"},
		{AuthorID: deletedAuthor.ID, Content: "deleted author", Visibility: "public"},
	}
	for index := range posts {
		posts[index].CreatedAt = now.Add(time.Duration(index) * time.Second)
		posts[index].UpdatedAt = posts[index].CreatedAt
	}
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	rootID := posts[0].ID
	if err := db.Model(&models.Post{}).Where("id = ?", posts[4].ID).Updates(map[string]interface{}{
		"reply_to_post_id": rootID,
		"conversation_id":  rootID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	embeddingRows := []models.PostEmbedding{
		coverageIntegrationEmbedding(posts[0], "post_embedding_v2"),
		coverageIntegrationEmbedding(posts[1], "post_embedding_v2"),
		coverageIntegrationEmbedding(posts[2], "post_embedding_v1"),
	}
	if err := db.Create(&embeddingRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&posts[3]).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{activeAuthor.ID, deletedAuthor.ID}).Delete(&models.User{})
	})

	coverage, err := calculatePostEmbeddingCoverage(context.Background(), gormPostEmbeddingCoverageScanner{db: db}, "post_embedding_v2")
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 3 || coverage.Ready != 2 || coverage.Missing != 1 || coverage.Percent < 66.6666 || coverage.Percent > 66.6667 {
		t.Fatalf("coverage=%+v, want eligible=3 ready=2 missing=1", coverage)
	}
}

func coverageIntegrationEmbedding(post models.Post, version string) models.PostEmbedding {
	return models.PostEmbedding{
		PostID: post.ID, Version: version, Model: "coverage-integration-model", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(post.Content),
	}
}
