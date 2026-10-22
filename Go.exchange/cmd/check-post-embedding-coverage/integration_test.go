package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

func TestPostEmbeddingCoverageCountsOnlyEligibleRootsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	posts := []models.Post{
		{AuthorID: activeAuthor.ID, Content: "seven day boundary ready", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "recent stale", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "v1 only", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "recent missing", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "deleted post", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "reply", Visibility: "public"},
		{AuthorID: deletedAuthor.ID, Content: "deleted author", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "older than seven days", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "future post", Visibility: "public"},
		{AuthorID: activeAuthor.ID, Content: "five day boundary ready", Visibility: "public"},
	}
	for index := range posts {
		posts[index].CreatedAt = now.Add(-time.Duration(index+1) * time.Minute)
		posts[index].UpdatedAt = posts[index].CreatedAt
	}
	posts[0].CreatedAt = now.AddDate(0, 0, -7)
	posts[0].UpdatedAt = posts[0].CreatedAt
	posts[7].CreatedAt = now.AddDate(0, 0, -7).Add(-time.Second)
	posts[7].UpdatedAt = posts[7].CreatedAt
	posts[8].CreatedAt = now.Add(time.Second)
	posts[8].UpdatedAt = posts[8].CreatedAt
	posts[9].CreatedAt = now.AddDate(0, 0, -5)
	posts[9].UpdatedAt = posts[9].CreatedAt
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	rootID := posts[0].ID
	if err := db.Model(&models.Post{}).Where("id = ?", posts[5].ID).Updates(map[string]interface{}{
		"reply_to_post_id": rootID,
		"conversation_id":  rootID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	embeddingRows := []models.PostEmbedding{
		coverageIntegrationEmbedding(posts[0], "post_embedding_v2"),
		coverageIntegrationEmbedding(posts[1], "post_embedding_v2"),
		coverageIntegrationEmbedding(posts[2], "post_embedding_v1"),
		coverageIntegrationEmbedding(posts[9], "post_embedding_v2"),
	}
	embeddingRows[1].ContentHash = strings.Repeat("0", 64)
	if err := db.Create(&embeddingRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&posts[4]).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&posts[6]).Error; err != nil {
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

	coverage, err := calculatePostEmbeddingCoverage(context.Background(), gormPostEmbeddingCoverageScanner{db: db}, "post_embedding_v2", now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 5 || coverage.Ready != 2 || coverage.Missing != 3 || coverage.StaleContent != 1 || coverage.Percent < 39.9999 || coverage.Percent > 40.0001 || coverage.PrimaryWindowDays != 7 {
		t.Fatalf("coverage=%+v, want eligible=5 ready=2 missing=3 stale=1 at seven-day window", coverage)
	}
	var pages, scannedRows int
	windowStart, windowEnd := config.RecommendationPrimaryWindowBounds(now, 7)
	if err := (gormPostEmbeddingCoverageScanner{db: db}).ScanEligible(context.Background(), "post_embedding_v2", windowStart, windowEnd, 2, func(rows []postEmbeddingCoverageRow) error {
		pages++
		scannedRows += len(rows)
		return nil
	}); err != nil {
		t.Fatalf("scan eligible Posts in keyset pages: %v", err)
	}
	if pages != 3 || scannedRows != 5 {
		t.Fatalf("coverage pages=%d scanned=%d, want pages=3 scanned=5", pages, scannedRows)
	}
	fiveDayCoverage, err := calculatePostEmbeddingCoverage(context.Background(), gormPostEmbeddingCoverageScanner{db: db}, "post_embedding_v2", now, 5)
	if err != nil {
		t.Fatal(err)
	}
	if fiveDayCoverage.Eligible != 4 || fiveDayCoverage.Ready != 1 || fiveDayCoverage.StaleContent != 1 || fiveDayCoverage.PrimaryWindowDays != 5 {
		t.Fatalf("five-day coverage=%+v, want boundary five-day fixture included", fiveDayCoverage)
	}
	if err := db.Model(&models.PostEmbedding{}).
		Where("post_id = ? AND version = ?", posts[1].ID, "post_embedding_v2").
		Update("content_hash", embeddings.PostEmbeddingContentHash(posts[1].Content)).Error; err != nil {
		t.Fatalf("simulate regenerated matching embedding: %v", err)
	}
	coverage, err = calculatePostEmbeddingCoverage(context.Background(), gormPostEmbeddingCoverageScanner{db: db}, "post_embedding_v2", now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Eligible != 5 || coverage.Ready != 3 || coverage.Missing != 2 || coverage.StaleContent != 0 || coverage.Percent < 59.9999 || coverage.Percent > 60.0001 {
		t.Fatalf("regenerated coverage=%+v, want eligible=5 ready=3 missing=2 stale=0", coverage)
	}
}

func coverageIntegrationEmbedding(post models.Post, version string) models.PostEmbedding {
	return models.PostEmbedding{
		PostID: post.ID, Version: version, Model: "coverage-integration-model", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(post.Content),
	}
}
