package controllers

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openRecommendationCandidateIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.UserFollow{},
		&models.Post{},
		&models.PostArticle{},
		&models.PostBehavior{},
		&models.PostReaction{},
	); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = nil
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})
	return db
}

func newRecommendationCandidateIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{
		Username: "recommendation-candidate-" + label + "-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	return user
}

func newRecommendationCandidateIntegrationArticle(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := models.Post{
		Model:    gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt},
		AuthorID: author.ID, Content: "body", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostArticle{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
	})
	if err := db.Create(&models.PostArticle{PostID: article.ID, Title: title, Preview: "body", PublicationState: consts.PostPublicationStatePublished, PublishedAt: &publishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func cleanupRecommendationCandidateIntegrationData(db *gorm.DB, postIDs, userIDs []uint) {
	db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostArticle{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
	db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
}

func TestLoadRecommendationCandidateSetUsesEqualRRFFusionIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "rrf-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "rrf-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	article := newRecommendationCandidateIntegrationArticle(t, db, author, "rrf", now)
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Update("like_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{article.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	candidateSet, err := loadRecommendationCandidateSet(viewer.ID, userInterestProfile{}, map[uint]servedPost{}, now, defaultRecommendationConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidateSet.Candidates) != 1 || candidateSet.Candidates[0].PostID != article.ID {
		t.Fatalf("candidate set=%#v, want one RRF-fused post", candidateSet)
	}
	candidate := candidateSet.Candidates[0]
	if !candidate.FromFollowing || !candidate.FromRecent || !candidate.FromTrending || candidate.SourceCount != 3 || candidate.FollowingRank != 1 || candidate.RecentRank != 1 || candidate.TrendingRank != 1 {
		t.Fatalf("fused candidate metadata=%#v", candidate)
	}
	if want := 3.0 / 61; math.Abs(candidate.FusionScore-want) > 1e-12 {
		t.Fatalf("fusion score=%v want=%v", candidate.FusionScore, want)
	}
}

func TestRecommendationRecallSkipsDeletedAuthorBeforeLimitIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	goodArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "good", now.Add(-time.Minute))
	postIDs := []uint{badArticle.ID, goodArticle.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := loadRecommendationSourceCandidates(
		viewer.ID,
		userInterestProfile{},
		map[uint]servedPost{},
		now,
		defaultRecommendationConfig(),
		false,
		effectivePublishedAtSQL("posts", "pa_recommendation")+" DESC, posts.id DESC",
		1,
		"recent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count=%d, want 1", len(candidates))
	}
	if candidates[0].PostID != goodArticle.ID {
		t.Fatalf("candidate article ID=%d, want valid article %d", candidates[0].PostID, goodArticle.ID)
	}
	if candidates[0].PostID == badArticle.ID {
		t.Fatal("deleted-author article was returned")
	}
}

func TestRecommendationHydrationDiscardsDeletedAuthorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "valid", now)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	postIDs := []uint{validArticle.ID, badArticle.ID}
	userIDs := []uint{validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	originalLoader := loadRecommendationPostEmbeddings
	var embeddingPostIDs []uint
	loadRecommendationPostEmbeddings = func(postIDs []uint, _ string) (map[uint][]float32, error) {
		embeddingPostIDs = append([]uint(nil), postIDs...)
		return map[uint][]float32{}, nil
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{
			{PostID: validArticle.ID, FromRecent: true},
			{PostID: badArticle.ID, FromTrending: true},
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("hydrated count=%d, want 1", len(hydrated))
	}
	if hydrated[0].Post.ID != validArticle.ID {
		t.Fatalf("hydrated article ID=%d, want %d", hydrated[0].Post.ID, validArticle.ID)
	}
	if hydrated[0].Post.Author.ID != validAuthor.ID || hydrated[0].Post.Author.ID != hydrated[0].Post.AuthorID {
		t.Fatalf("hydrated author=%#v author_id=%d", hydrated[0].Post.Author, hydrated[0].Post.AuthorID)
	}
	if len(embeddingPostIDs) != 1 || embeddingPostIDs[0] != validArticle.ID {
		t.Fatalf("embedding article IDs=%v, want [%d]", embeddingPostIDs, validArticle.ID)
	}
}

func TestRecommendationHydrationAllInvalidAuthorsReturnsEmptyIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationArticle(t, db, deletedAuthor, "bad", now)
	postIDs := []uint{badArticle.ID}
	userIDs := []uint{deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	originalLoader := loadRecommendationPostEmbeddings
	called := false
	loadRecommendationPostEmbeddings = func(_ []uint, _ string) (map[uint][]float32, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{{PostID: badArticle.ID}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 0 {
		t.Fatalf("hydrated count=%d, want 0", len(hydrated))
	}
	if called {
		t.Fatal("embedding loader was called for all-invalid candidates")
	}
}

func TestRecommendationHydrationPropagatesEmbeddingErrorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationArticle(t, db, validAuthor, "valid", now)
	postIDs := []uint{validArticle.ID}
	userIDs := []uint{validAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	sentinel := errors.New("embedding load failure")
	originalLoader := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(postIDs []uint, _ string) (map[uint][]float32, error) {
		if len(postIDs) != 1 || postIDs[0] != validArticle.ID {
			t.Fatalf("embedding article IDs=%v, want [%d]", postIDs, validArticle.ID)
		}
		return nil, sentinel
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	_, err := hydrateRecommendationCandidates(
		[]embeddingCandidate{{PostID: validArticle.ID}},
		now,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want sentinel", err)
	}
}
