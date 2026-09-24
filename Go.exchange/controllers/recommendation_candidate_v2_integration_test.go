package controllers

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
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
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.UserFollow{},
		&models.Post{},
		&models.PostEmbedding{},
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
	if err := initialize.RunMigrations(); err != nil {
		t.Fatal(err)
	}
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

func newRecommendationCandidateIntegrationPost(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, title, publishedAt)
	embedding := models.PostEmbedding{
		PostID: article.ID, Version: config.ServingEmbeddingVersion(), Model: "recommendation-candidate-test",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}),
		ContentHash: embeddings.PostEmbeddingContentHash(article.Content),
	}
	if err := db.Create(&embedding).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func newRecommendationCandidateIntegrationPostWithoutEmbedding(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := models.Post{
		Model:    gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt},
		AuthorID: author.ID, Content: "body", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
	})
	return article
}

func cleanupRecommendationCandidateIntegrationData(db *gorm.DB, postIDs, userIDs []uint) {
	db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
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
	article := newRecommendationCandidateIntegrationPost(t, db, author, "rrf", now)
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Update("like_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{article.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	candidateSet, err := loadRecommendationCandidateSet(db, viewer.ID, userInterestProfile{}, map[uint]servedPost{}, now, defaultRecommendationConfig(), false)
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

func TestRecommendationCandidateSourcesRequireServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "serving-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "serving-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	v1Only := newRecommendationCandidateIntegrationPost(t, db, author, "v1-only", now.Add(-time.Minute))
	v1AndV2 := newRecommendationCandidateIntegrationPost(t, db, author, "v1-and-v2", now)
	if err := db.Create(&models.PostEmbedding{
		PostID: v1AndV2.ID, Version: "post_embedding_v2", Model: "candidate-v2", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(v1AndV2.Content),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for _, post := range []models.Post{v1Only, v1AndV2} {
		if err := db.Model(&models.Post{}).Where("id = ?", post.ID).Update("like_count", 1).Error; err != nil {
			t.Fatal(err)
		}
	}
	postIDs := []uint{v1Only.ID, v1AndV2.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{
		ServingVersion: "post_embedding_v2",
		BuildVersion:   "post_embedding_v2",
	}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	served := map[uint]servedPost{}
	cfg := defaultRecommendationConfig()
	assertOnlyV2 := func(source string, candidates []embeddingCandidate, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s candidates: %v", source, err)
		}
		if len(candidates) != 1 || candidates[0].PostID != v1AndV2.ID {
			t.Fatalf("%s candidates=%v, want only v1+v2 post %d", source, candidateIDs(candidates), v1AndV2.ID)
		}
	}
	following, err := loadRecommendationFollowingCandidates(db, viewer.ID, profile, served, now, cfg, false, 10)
	assertOnlyV2("following", following, err)
	recent, err := loadRecommendationSourceCandidates(db, viewer.ID, profile, served, now, cfg, false, "posts.created_at DESC, posts.id DESC", 10, "recent")
	assertOnlyV2("recent", recent, err)
	trending, err := loadRecommendationTrendingCandidates(db, viewer.ID, profile, served, now, cfg, false, 10)
	assertOnlyV2("trending", trending, err)
	semantic, err := loadRecommendationSemanticPool(db, viewer.ID, profile, served, now, false, time.Time{}, "", 10, nil)
	assertOnlyV2("semantic", semantic, err)
	publicRecent, err := loadPublicRecommendationSourceCandidates(db, now, cfg, "posts.created_at DESC, posts.id DESC", 10, "recent", nil)
	assertOnlyV2("public recent", publicRecent, err)
	publicTrending, err := loadPublicRecommendationTrendingCandidates(db, now, cfg, 10, nil)
	assertOnlyV2("public trending", publicTrending, err)
}

func TestRecommendationPreSwitchContinuesUsingV1Integration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "pre-switch-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "pre-switch-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	v1Only := newRecommendationCandidateIntegrationPost(t, db, author, "v1-only", now)
	v2Only := newRecommendationCandidateIntegrationPost(t, db, author, "v2-only", now.Add(time.Minute))
	if err := db.Unscoped().Where("post_id = ? AND version = ?", v2Only.ID, "post_embedding_v1").Delete(&models.PostEmbedding{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostEmbedding{
		PostID: v2Only.ID, Version: "post_embedding_v2", Model: "candidate-v2", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(v2Only.Content),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{v1Only.ID, v2Only.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Embedding: config.EmbeddingConfig{
		ServingVersion: "post_embedding_v1",
		BuildVersion:   "post_embedding_v2",
	}}
	t.Cleanup(func() { config.AppConfig = originalConfig })
	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	served := map[uint]servedPost{}
	authenticated, err := loadRecommendationSourceCandidates(db, viewer.ID, profile, served, now, cfg, false, "posts.created_at DESC, posts.id DESC", 10, "recent")
	if err != nil {
		t.Fatal(err)
	}
	public, err := loadPublicRecommendationSourceCandidates(db, now, cfg, "posts.created_at DESC, posts.id DESC", 10, "recent", nil)
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := loadRecommendationSemanticPool(db, viewer.ID, profile, served, now, false, time.Time{}, "", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		name       string
		candidates []embeddingCandidate
	}{{"authenticated recent", authenticated}, {"public recent", public}, {"semantic", semantic}} {
		if len(source.candidates) != 1 || source.candidates[0].PostID != v1Only.ID {
			t.Fatalf("%s candidates=%v, want only v1 post %d", source.name, candidateIDs(source.candidates), v1Only.ID)
		}
	}
}

func TestLoadPublicRecommendationCandidateSetUsesOnlyEligiblePublicRootsIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "public-author")
	now := time.Now().UTC()
	recentRoot := newRecommendationCandidateIntegrationPost(t, db, author, "recent-root", now.Add(-10*time.Minute))
	trendingRoot := newRecommendationCandidateIntegrationPost(t, db, author, "trending-root", now.Add(-30*time.Minute))
	if err := db.Model(&models.Post{}).Where("id = ?", trendingRoot.ID).Update("like_count", 5).Error; err != nil {
		t.Fatal(err)
	}

	deletedRoot := newRecommendationCandidateIntegrationPost(t, db, author, "deleted-root", now.Add(-5*time.Minute))
	conversationID := recentRoot.ID
	replyRootID := recentRoot.ID
	reply := models.Post{
		Model:          gorm.Model{CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
		AuthorID:       author.ID,
		Content:        "reply-root",
		Visibility:     "public",
		ConversationID: &conversationID,
		ReplyToPostID:  &replyRootID,
	}
	if err := db.Delete(&deletedRoot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{recentRoot.ID, trendingRoot.ID, deletedRoot.ID, reply.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	})

	cfg := defaultRecommendationConfig()
	cfg.Candidates.ColdStart.Recent = 100
	cfg.Candidates.ColdStart.Trending = 100
	cfg.Candidates.ColdStart.Merged = 200

	candidateSet, err := loadPublicRecommendationCandidateSet(db, now, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[uint]embeddingCandidate, len(candidateSet.Candidates))
	for _, candidate := range candidateSet.Candidates {
		byID[candidate.PostID] = candidate
	}
	for _, want := range []uint{recentRoot.ID, trendingRoot.ID} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("eligible public root %d missing from candidates=%v", want, candidateIDs(candidateSet.Candidates))
		}
	}
	for _, excluded := range []uint{deletedRoot.ID, reply.ID} {
		if _, ok := byID[excluded]; ok {
			t.Fatalf("ineligible post %d was returned in candidates=%v", excluded, candidateIDs(candidateSet.Candidates))
		}
	}
	if !byID[recentRoot.ID].FromRecent {
		t.Fatalf("recent root candidate metadata=%#v", byID[recentRoot.ID])
	}
	if !byID[trendingRoot.ID].FromRecent || !byID[trendingRoot.ID].FromTrending {
		t.Fatalf("trending root candidate metadata=%#v", byID[trendingRoot.ID])
	}
	if candidateSet.FollowingCount != 0 {
		t.Fatalf("anonymous following count=%d, want 0", candidateSet.FollowingCount)
	}
	if candidateSet.SemanticCount != 0 {
		t.Fatalf("anonymous semantic count=%d, want 0", candidateSet.SemanticCount)
	}

	excludedSet, err := loadPublicRecommendationCandidateSet(db, now, cfg, map[uint]struct{}{recentRoot.ID: {}})
	if err != nil {
		t.Fatal(err)
	}
	if containsRecommendationCandidateID(excludedSet.Candidates, recentRoot.ID) {
		t.Fatalf("explicitly excluded post %d was returned", recentRoot.ID)
	}
}

func candidateIDs(candidates []embeddingCandidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.PostID)
	}
	return ids
}

func containsRecommendationCandidateID(candidates []embeddingCandidate, want uint) bool {
	for _, candidate := range candidates {
		if candidate.PostID == want {
			return true
		}
	}
	return false
}

func TestRecommendationRecallSkipsDeletedAuthorBeforeLimitIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
	goodArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "good", now.Add(-time.Minute))
	postIDs := []uint{badArticle.ID, goodArticle.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := loadRecommendationSourceCandidates(
		db,
		viewer.ID,
		userInterestProfile{},
		map[uint]servedPost{},
		now,
		defaultRecommendationConfig(),
		false,
		"posts.created_at DESC, posts.id DESC",
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
	validArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "valid", now)
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
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
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, postIDs []uint, _ string) (map[uint][]float32, error) {
		embeddingPostIDs = append([]uint(nil), postIDs...)
		return map[uint][]float32{validArticle.ID: {1, 0}}, nil
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		db,
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
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
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
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, _ string) (map[uint][]float32, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	hydrated, err := hydrateRecommendationCandidates(
		db,
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

func TestRecommendationHydrationDropsCandidateWithoutServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "missing-serving-embedding")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	article := newRecommendationCandidateIntegrationPost(t, db, author, "missing-serving-embedding", now)
	postIDs := []uint{article.ID}
	userIDs := []uint{author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	originalLoader := loadRecommendationPostEmbeddings
	var requestedVersion string
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, _ []uint, version string) (map[uint][]float32, error) {
		requestedVersion = version
		return map[uint][]float32{}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = originalLoader })

	hydrated, err := hydrateRecommendationCandidates(db, []embeddingCandidate{{PostID: article.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 0 || requestedVersion != config.ServingEmbeddingVersion() {
		t.Fatalf("hydrated=%+v requested_version=%q want=%q", hydrated, requestedVersion, config.ServingEmbeddingVersion())
	}
}

func TestRecommendationHydrationPropagatesEmbeddingErrorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "valid", now)
	postIDs := []uint{validArticle.ID}
	userIDs := []uint{validAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	sentinel := errors.New("embedding load failure")
	originalLoader := loadRecommendationPostEmbeddings
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, postIDs []uint, _ string) (map[uint][]float32, error) {
		if len(postIDs) != 1 || postIDs[0] != validArticle.ID {
			t.Fatalf("embedding article IDs=%v, want [%d]", postIDs, validArticle.ID)
		}
		return nil, sentinel
	}
	t.Cleanup(func() {
		loadRecommendationPostEmbeddings = originalLoader
	})

	_, err := hydrateRecommendationCandidates(
		db,
		[]embeddingCandidate{{PostID: validArticle.ID}},
		now,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want sentinel", err)
	}
}
