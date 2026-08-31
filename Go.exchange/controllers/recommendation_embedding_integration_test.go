package controllers

import (
	"os"
	"testing"
	"time"

	"Go.exchange/config"
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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.PostEmbedding{}, &models.PostBehavior{}, &models.PostReaction{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "post_embedding_v1"}}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	viewer := models.User{Username: "semantic-viewer-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "semantic-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeArticle := func(authorID uint, title string) models.Post {
		article := models.Post{AuthorID: authorID, Content: title + " body", Visibility: "public", Model: gorm.Model{CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.PostArticle{PostID: article.ID, Title: title, Preview: title, PublicationState: "published", PublishedAt: &now}).Error; err != nil {
			t.Fatal(err)
		}
		return article
	}
	nearest := makeArticle(author.ID, "nearest")
	orthogonal := makeArticle(author.ID, "orthogonal")
	interacted := makeArticle(author.ID, "interacted")
	selfArticle := makeArticle(viewer.ID, "self")
	for _, item := range []struct {
		article models.Post
		vector  []float32
	}{{nearest, []float32{1, 0}}, {orthogonal, []float32{0, 1}}, {interacted, []float32{1, 0}}, {selfArticle, []float32{1, 0}}} {
		if err := db.Create(&models.PostEmbedding{PostID: item.article.ID, Version: "post_embedding_v1", Model: "test", Dimensions: 2, Embedding: pgvector.NewVector(item.vector), ContentHash: item.article.Content}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.PostBehavior{UserID: viewer.ID, PostID: interacted.ID, Action: PostBehaviorActionView, LastSeenAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		postIDs := []uint{nearest.ID, orthogonal.ID, interacted.ID, selfArticle.ID}
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{viewer.ID, author.ID}).Delete(&models.User{})
	})

	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}, InteractedPostIDs: map[uint]struct{}{interacted.ID: {}}}
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, profile, map[uint]servedPost{}, now, cfg, false, cfg.Candidates.Personalized.Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates=%#v, want exactly 2 eligible semantic candidates", candidates)
	}
	if candidates[0].PostID != nearest.ID {
		t.Fatalf("first candidate=%d similarity=%f, want nearest=%d; candidates=%#v", candidates[0].PostID, candidates[0].PositiveSemanticSimilarity, nearest.ID, candidates)
	}
	if candidates[1].PostID != orthogonal.ID {
		t.Fatalf("second candidate=%d, want orthogonal=%d; candidates=%#v", candidates[1].PostID, orthogonal.ID, candidates)
	}
	for _, candidate := range candidates {
		if candidate.PostID == interacted.ID {
			t.Fatal("interacted article was recalled")
		}
		if candidate.PostID == selfArticle.ID {
			t.Fatal("self-authored article was recalled")
		}
	}
	if candidates[0].PositiveSemanticSimilarity < .99 {
		t.Fatalf("similarity=%f", candidates[0].PositiveSemanticSimilarity)
	}
}

func TestSemanticEmbeddingRecallFiltersActiveVersionIntegration(t *testing.T) {
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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.PostEmbedding{}, &models.PostBehavior{}, &models.PostReaction{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{Version: "v2"}}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})

	viewer := models.User{Username: "semantic-version-viewer-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "semantic-version-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldArticle := models.Post{AuthorID: author.ID, Content: "old", Visibility: "public", Model: gorm.Model{CreatedAt: now, UpdatedAt: now}}
	activeArticle := models.Post{AuthorID: author.ID, Content: "active", Visibility: "public", Model: gorm.Model{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&oldArticle).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&activeArticle).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		postID  uint
		version string
	}{
		{oldArticle.ID, "v1"},
		{activeArticle.ID, "v2"},
	} {
		if err := db.Create(&models.PostEmbedding{
			PostID: item.postID, Version: item.version, Model: "test", Dimensions: 2,
			Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: item.version,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ids := []uint{oldArticle.ID, activeArticle.ID}
		db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{viewer.ID, author.ID}).Delete(&models.User{})
	})

	cfg := defaultRecommendationConfig()
	candidates, err := loadRecommendationSemanticCandidates(viewer.ID, userInterestProfile{
		PositiveVector: []float32{1, 0},
	}, map[uint]servedPost{}, now, cfg, false, cfg.Candidates.Personalized.Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].PostID != activeArticle.ID {
		t.Fatalf("candidates=%#v", candidates)
	}
}
