package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

func TestRequeuePostEmbeddingsIntegration(t *testing.T) {
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

	user := models.User{Username: "embedding-requeue-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	posts := []models.Post{}
	var postIDs []uint
	t.Cleanup(func() {
		ids := make([]uint, 0, len(posts)+len(postIDs))
		for _, post := range posts {
			if post.ID != 0 {
				ids = append(ids, post.ID)
			}
		}
		ids = append(ids, postIDs...)
		var authoredIDs []uint
		db.Unscoped().Model(&models.Post{}).Where("author_id = ?", user.ID).Pluck("id", &authoredIDs)
		ids = append(ids, authoredIDs...)
		if len(ids) > 0 {
			db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostEmbedding{})
			db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
		}
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	posts = []models.Post{
		{AuthorID: user.ID, Content: "missing", Visibility: "public"},
		{AuthorID: user.ID, Content: "current", Visibility: "public"},
		{AuthorID: user.ID, Content: "stale version", Visibility: "public"},
		{AuthorID: user.ID, Content: "stale content", Visibility: "public"},
		{AuthorID: user.ID, Content: "ordinary short post", Visibility: "public"},
		{AuthorID: user.ID, Content: "another ordinary short post", Visibility: "public"},
		{AuthorID: user.ID, Content: "deleted", Visibility: "public"},
		{AuthorID: user.ID, Content: "seven-day boundary", Visibility: "public"},
		{AuthorID: user.ID, Content: "five-day boundary", Visibility: "public"},
		{AuthorID: user.ID, Content: "older than seven days", Visibility: "public"},
		{AuthorID: user.ID, Content: "future", Visibility: "public"},
	}
	for index := range posts {
		posts[index].CreatedAt = now.Add(-time.Duration(index+1) * time.Minute)
		posts[index].UpdatedAt = posts[index].CreatedAt
	}
	posts[7].CreatedAt = now.AddDate(0, 0, -7)
	posts[7].UpdatedAt = posts[7].CreatedAt
	posts[8].CreatedAt = now.AddDate(0, 0, -5)
	posts[8].UpdatedAt = posts[8].CreatedAt
	posts[9].CreatedAt = now.AddDate(0, 0, -7).Add(-time.Second)
	posts[9].UpdatedAt = posts[9].CreatedAt
	posts[10].CreatedAt = now.Add(time.Second)
	posts[10].UpdatedAt = posts[10].CreatedAt
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}
	currentHash := embeddings.PostEmbeddingContentHash(posts[1].Content)
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

	publisher := &reconciliationTestPublisher{}
	stats, err := requeuePostEmbeddings(context.Background(), db, publisher, "v2", now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 8 || stats.Missing != 6 || stats.StaleContent != 1 || stats.Published != 7 {
		t.Fatalf("stats=%+v", stats)
	}
	if publisher.calls != 1 || len(publisher.events) != 7 {
		t.Fatalf("publisher calls=%d events=%d", publisher.calls, len(publisher.events))
	}
	gotIDs := make(map[uint]bool, len(publisher.events))
	for _, event := range publisher.events {
		var payload eventing.PostEmbeddingRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		gotIDs[payload.PostID] = true
	}
	for _, index := range []int{0, 2, 3, 4, 5, 7, 8} {
		if !gotIDs[posts[index].ID] {
			t.Fatalf("missing embedding event for post %d", posts[index].ID)
		}
	}
	for _, index := range []int{1, 6, 9, 10} {
		if gotIDs[posts[index].ID] {
			t.Fatalf("unexpected embedding event for post %d", posts[index].ID)
		}
	}

	fiveDayPublisher := &reconciliationTestPublisher{}
	fiveDayStats, err := requeuePostEmbeddings(context.Background(), db, fiveDayPublisher, "v2", now, 5)
	if err != nil {
		t.Fatal(err)
	}
	if fiveDayStats.Scanned != 7 || fiveDayStats.Missing != 5 || fiveDayStats.StaleContent != 1 || fiveDayStats.Published != 6 {
		t.Fatalf("five-day stats=%+v", fiveDayStats)
	}
	fiveDayIDs := make(map[uint]bool, len(fiveDayPublisher.events))
	for _, event := range fiveDayPublisher.events {
		var payload eventing.PostEmbeddingRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		fiveDayIDs[payload.PostID] = true
	}
	if !fiveDayIDs[posts[8].ID] || fiveDayIDs[posts[7].ID] || fiveDayIDs[posts[9].ID] || fiveDayIDs[posts[10].ID] {
		t.Fatalf("five-day reconciliation included wrong boundaries: %v", fiveDayIDs)
	}
}
