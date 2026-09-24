package controllers

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/embeddingstate"
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
	previousServingVersion, err := embeddingstate.LoadServingVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := embeddingstate.SetServingVersion(context.Background(), db, previousServingVersion); err != nil {
			t.Errorf("restore embedding serving version: %v", err)
		}
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

func newRecommendationCandidateIntegrationPost(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, title, publishedAt)
	embedding := models.PostEmbedding{
		PostID: article.ID, Version: "post_embedding_v1", Model: "recommendation-candidate-test",
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

	candidateSet, err := loadRecommendationCandidateSet(db, "post_embedding_v1", viewer.ID, userInterestProfile{}, map[uint]servedPost{}, now, defaultRecommendationConfig(), false)
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

func TestRecommendationCandidateSourcesOnlyRequireEmbeddingForSemanticRecallIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "serving-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "serving-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	noEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "no-embedding", now.Add(-2*time.Minute))
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
	postIDs := []uint{noEmbedding.ID, v1Only.ID, v1AndV2.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	servingVersion := "post_embedding_v2"
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	served := map[uint]servedPost{}
	cfg := defaultRecommendationConfig()
	wantNonSemanticIDs := []uint{noEmbedding.ID, v1Only.ID, v1AndV2.ID}
	assertNonSemanticSource := func(source string, candidates []embeddingCandidate, err error, hasSource func(embeddingCandidate) bool) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s candidates: %v", source, err)
		}
		byID := make(map[uint]embeddingCandidate, len(candidates))
		for _, candidate := range candidates {
			byID[candidate.PostID] = candidate
		}
		for _, postID := range wantNonSemanticIDs {
			candidate, ok := byID[postID]
			if !ok {
				t.Fatalf("%s candidates=%v, want fixture post %d", source, candidateIDs(candidates), postID)
			}
			if hasSource != nil && !hasSource(candidate) {
				t.Fatalf("%s candidate provenance=%#v, want source flag", source, candidate)
			}
		}
	}
	following, err := loadRecommendationFollowingCandidates(db, servingVersion, viewer.ID, profile, served, now, cfg, false, 10)
	assertNonSemanticSource("following", following, err, func(candidate embeddingCandidate) bool { return candidate.FromFollowing })
	recent, err := loadRecommendationSourceCandidates(db, servingVersion, viewer.ID, profile, served, now, cfg, false, "posts.created_at DESC, posts.id DESC", 10, "recent")
	assertNonSemanticSource("recent", recent, err, func(candidate embeddingCandidate) bool { return candidate.FromRecent })
	trending, err := loadRecommendationTrendingCandidates(db, servingVersion, viewer.ID, profile, served, now, cfg, false, 10)
	assertNonSemanticSource("trending", trending, err, func(candidate embeddingCandidate) bool { return candidate.FromTrending })
	semantic, err := loadRecommendationSemanticPool(db, servingVersion, viewer.ID, profile, served, now, false, time.Time{}, "", 10, nil)
	if err != nil {
		t.Fatalf("semantic candidates: %v", err)
	}
	if len(semantic) != 1 || semantic[0].PostID != v1AndV2.ID || !semantic[0].FromSemantic {
		t.Fatalf("semantic candidates=%v, want only serving-version post %d", candidateIDs(semantic), v1AndV2.ID)
	}
	publicRecent, err := loadPublicRecommendationSourceCandidates(db, servingVersion, now, cfg, "posts.created_at DESC, posts.id DESC", 10, "recent", nil)
	assertNonSemanticSource("public recent", publicRecent, err, func(candidate embeddingCandidate) bool { return candidate.FromRecent })
	publicTrending, err := loadPublicRecommendationTrendingCandidates(db, servingVersion, now, cfg, 10, nil)
	assertNonSemanticSource("public trending", publicTrending, err, func(candidate embeddingCandidate) bool { return candidate.FromTrending })
	fused, err := loadRecommendationCandidateSet(db, servingVersion, viewer.ID, profile, served, now, cfg, false)
	if err != nil {
		t.Fatalf("fused candidate set: %v", err)
	}
	fusedByID := make(map[uint]embeddingCandidate, len(fused.Candidates))
	for _, candidate := range fused.Candidates {
		fusedByID[candidate.PostID] = candidate
	}
	for _, postID := range []uint{noEmbedding.ID, v1Only.ID} {
		candidate, ok := fusedByID[postID]
		if !ok || candidate.FromSemantic || !candidate.FromFollowing || !candidate.FromRecent || !candidate.FromTrending ||
			candidate.SourceCount != 3 || candidate.FollowingRank <= 0 || candidate.RecentRank <= 0 || candidate.TrendingRank <= 0 {
			t.Fatalf("fused non-semantic candidate %d=%#v, want three-source provenance without semantic recall", postID, candidate)
		}
	}
}

func TestRecommendationSemanticRecallRequiresServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "semantic-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	noEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "semantic-no-embedding", now)
	wrongVersion := newRecommendationCandidateIntegrationPost(t, db, author, "semantic-wrong-version", now.Add(-time.Minute))
	servingVersionPost := newRecommendationCandidateIntegrationPost(t, db, author, "semantic-serving-version", now.Add(-2*time.Minute))
	if err := db.Create(&models.PostEmbedding{
		PostID: servingVersionPost.ID, Version: "post_embedding_v2", Model: "candidate-v2", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(servingVersionPost.Content),
	}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{noEmbedding.ID, wrongVersion.ID, servingVersionPost.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	candidates, err := loadRecommendationSemanticPool(
		db,
		"post_embedding_v2",
		viewer.ID,
		userInterestProfile{PositiveVector: []float32{1, 0}},
		map[uint]servedPost{},
		now,
		false,
		time.Time{},
		"",
		10,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].PostID != servingVersionPost.ID || !candidates[0].FromSemantic {
		t.Fatalf("semantic candidates=%v, want only serving-version post %d", candidateIDs(candidates), servingVersionPost.ID)
	}
}

func TestPublicRecommendationServingSelectsPostsWithoutEmbeddingsIncludingSoftFallbackIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	freshAuthor := newRecommendationCandidateIntegrationUser(t, db, "guest-fresh-author")
	softAuthor := newRecommendationCandidateIntegrationUser(t, db, "guest-soft-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	freshPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, freshAuthor, "guest-fresh-no-embedding", now.Add(-time.Minute))
	softPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, softAuthor, "guest-soft-no-embedding", now.Add(-2*time.Minute))
	postIDs := []uint{freshPost.ID, softPost.ID}
	userIDs := []uint{freshAuthor.ID, softAuthor.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	served := map[uint]servedPost{
		softPost.ID: {LastServedAt: now.Add(-24 * time.Hour), Soft: true},
	}
	outcome, err := servePublicRecommendationCandidatePath(
		context.Background(),
		db,
		2,
		defaultRecommendationConfig(),
		now,
		"guest-no-embedding-fallback",
		recommendationServingSnapshot{EmbeddingVersion: "post_embedding_v1"},
		recommendationLanguageContext{},
		served,
	)
	if err != nil {
		t.Fatal(err)
	}
	selectedByID := make(map[uint]selectedRecommendation, len(outcome.Selected))
	for _, item := range outcome.Selected {
		selectedByID[item.Post.ID] = item
	}
	if len(outcome.Selected) != 2 {
		t.Fatalf("guest selected=%v, want fresh and soft fallback posts", publicServingSelectedIDs(outcome.Selected))
	}
	if _, ok := selectedByID[freshPost.ID]; !ok {
		t.Fatalf("guest selected=%v, want no-embedding fresh post %d", publicServingSelectedIDs(outcome.Selected), freshPost.ID)
	}
	softFallback, ok := selectedByID[softPost.ID]
	if !ok || !softFallback.Candidate.WasSoftServed {
		t.Fatalf("guest selected=%#v, want no-embedding soft-served fallback post %d", outcome.Selected, softPost.ID)
	}
	if len(outcome.RecallSets) != 2 {
		t.Fatalf("guest recall sets=%d, want fresh and fallback passes", len(outcome.RecallSets))
	}
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

	servingVersion := "post_embedding_v1"
	cfg := defaultRecommendationConfig()
	profile := userInterestProfile{PositiveVector: []float32{1, 0}}
	served := map[uint]servedPost{}
	authenticated, err := loadRecommendationSourceCandidates(db, servingVersion, viewer.ID, profile, served, now, cfg, false, "posts.created_at DESC, posts.id DESC", 10, "recent")
	if err != nil {
		t.Fatal(err)
	}
	public, err := loadPublicRecommendationSourceCandidates(db, servingVersion, now, cfg, "posts.created_at DESC, posts.id DESC", 10, "recent", nil)
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := loadRecommendationSemanticPool(db, servingVersion, viewer.ID, profile, served, now, false, time.Time{}, "", 10, nil)
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

	candidateSet, err := loadPublicRecommendationCandidateSet(db, "post_embedding_v1", now, cfg, nil)
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

	excludedSet, err := loadPublicRecommendationCandidateSet(db, "post_embedding_v1", now, cfg, map[uint]struct{}{recentRoot.ID: {}})
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
		"post_embedding_v1",
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
		"post_embedding_v1",
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
		"post_embedding_v1",
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

func TestRecommendationHydrationPreservesCandidateWithoutServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "missing-serving-embedding")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "missing-serving-embedding", now)
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

	hydrated, err := hydrateRecommendationCandidates(db, "post_embedding_v1", []embeddingCandidate{{PostID: article.ID, FromRecent: true, RecentRank: 3, SourceCount: 1}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 1 || hydrated[0].Post.ID != article.ID || requestedVersion != "post_embedding_v1" {
		t.Fatalf("hydrated=%+v requested_version=%q, want one post %d with version post_embedding_v1", hydrated, requestedVersion, article.ID)
	}
	if hydrated[0].Embedding != nil && len(hydrated[0].Embedding) != 0 {
		t.Fatalf("embedding=%v, want nil/empty for missing serving embedding", hydrated[0].Embedding)
	}
	if !hydrated[0].Candidate.FromRecent || hydrated[0].Candidate.RecentRank != 3 || hydrated[0].Candidate.SourceCount != 1 {
		t.Fatalf("candidate provenance=%#v, want recent source rank 3 and source count 1", hydrated[0].Candidate)
	}
}

func TestRecommendationHydrationPreservesOrderWithAndWithoutServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "mixed-serving-embedding")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	withEmbedding := newRecommendationCandidateIntegrationPost(t, db, author, "with-serving-embedding", now)
	withoutEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "without-serving-embedding", now.Add(-time.Minute))
	postIDs := []uint{withEmbedding.ID, withoutEmbedding.ID}
	userIDs := []uint{author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	originalLoader := loadRecommendationPostEmbeddings
	var requestedIDs []uint
	loadRecommendationPostEmbeddings = func(_ *gorm.DB, ids []uint, version string) (map[uint][]float32, error) {
		if version != "post_embedding_v1" {
			t.Fatalf("requested embedding version=%q, want post_embedding_v1", version)
		}
		requestedIDs = append([]uint(nil), ids...)
		return map[uint][]float32{withEmbedding.ID: {0.6, 0.8}}, nil
	}
	t.Cleanup(func() { loadRecommendationPostEmbeddings = originalLoader })

	candidates := []embeddingCandidate{
		{PostID: withEmbedding.ID, FromTrending: true, TrendingRank: 1},
		{PostID: withoutEmbedding.ID, FromRecent: true, RecentRank: 2},
	}
	hydrated, err := hydrateRecommendationCandidates(db, "post_embedding_v1", candidates, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 2 {
		t.Fatalf("hydrated count=%d, want 2", len(hydrated))
	}
	if hydrated[0].Post.ID != withEmbedding.ID || hydrated[1].Post.ID != withoutEmbedding.ID {
		t.Fatalf("hydrated order=[%d %d], want [%d %d]", hydrated[0].Post.ID, hydrated[1].Post.ID, withEmbedding.ID, withoutEmbedding.ID)
	}
	if len(hydrated[0].Embedding) != 2 || hydrated[0].Embedding[0] != 0.6 || hydrated[0].Embedding[1] != 0.8 {
		t.Fatalf("first embedding=%v, want [0.6 0.8]", hydrated[0].Embedding)
	}
	if hydrated[1].Embedding != nil && len(hydrated[1].Embedding) != 0 {
		t.Fatalf("second embedding=%v, want nil/empty", hydrated[1].Embedding)
	}
	if !hydrated[0].Candidate.FromTrending || hydrated[0].Candidate.TrendingRank != 1 || !hydrated[1].Candidate.FromRecent || hydrated[1].Candidate.RecentRank != 2 {
		t.Fatalf("candidate provenance=[%#v %#v], want original source metadata", hydrated[0].Candidate, hydrated[1].Candidate)
	}
	if len(requestedIDs) != 2 || requestedIDs[0] != withEmbedding.ID || requestedIDs[1] != withoutEmbedding.ID {
		t.Fatalf("embedding loader IDs=%v, want candidate order %v", requestedIDs, postIDs)
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
		"post_embedding_v1",
		[]embeddingCandidate{{PostID: validArticle.ID}},
		now,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want sentinel", err)
	}
}
