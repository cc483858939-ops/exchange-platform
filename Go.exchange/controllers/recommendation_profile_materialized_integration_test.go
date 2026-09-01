package controllers

import (
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const recommendationProfileControllerIntegrationEmbeddingVersion = "p1a-follow-up-controller-v1"

func openRecommendationProfileControllerIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN unavailable")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{
		Embedding: config.EmbeddingConfig{Version: recommendationProfileControllerIntegrationEmbeddingVersion},
	}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})
	if err := initialize.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	return db
}

func newRecommendationProfileControllerIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{Username: "p1a-follow-up-controller-" + label + "-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfile{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserPostRecoState{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserAuthorAffinity{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	return user
}

func newRecommendationProfileControllerIntegrationArticle(t *testing.T, db *gorm.DB, authorID uint, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := models.Post{
		Model: gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt}, AuthorID: authorID,
		Content: title + " body", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.UserPostRecoState{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostArticle{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
	})
	if err := db.Create(&models.PostArticle{PostID: article.ID, Title: title, Preview: title + " preview", PublicationState: "published", PublishedAt: &publishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func controllerIntegrationVector(values []float32) *pgvector.Vector {
	vector := pgvector.NewVector(values)
	return &vector
}

func compatibleControllerIntegrationProfile(userID uint, cfg config.RecommendationConfig, nextRebuildAt, computedAt time.Time) models.UserRecoProfile {
	return models.UserRecoProfile{
		UserID: userID, ProfileVersion: recommendation.MaterializedProfileVersion,
		ProfileConfigHash: recommendation.ProfileConfigHash(cfg, config.ActiveEmbeddingVersion()),
		EmbeddingVersion:  config.ActiveEmbeddingVersion(), Dimensions: 2,
		PositiveVector:   controllerIntegrationVector([]float32{1, 0}),
		NegativeVector:   controllerIntegrationVector([]float32{0, 1}),
		NegativeEvidence: 6, PositiveSignalCount: 1, NegativeSignalCount: 1,
		PersonalizedSignalCount: 2, ComputedAt: computedAt, NextRebuildAt: nextRebuildAt,
		UpdatedAt: computedAt,
	}
}

func TestMaterializedProfileLoaderStateMachineIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	cfg := normalizedRecommendationConfig()

	t.Run("hit", func(t *testing.T) {
		user := newRecommendationProfileControllerIntegrationUser(t, db, "loader-hit")
		profile := compatibleControllerIntegrationProfile(user.ID, cfg, now.Add(time.Hour), now.Add(-time.Minute))
		if err := db.Create(&profile).Error; err != nil {
			t.Fatal(err)
		}
		loaded, err := loadMaterializedUserInterestProfile(user.ID, now, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ProfileStatus != recommendationProfileStatusHit || !loaded.MaterializedInteractionsReady || len(loaded.PositiveVector) != 2 || len(loaded.NegativeVector) != 2 {
			t.Fatalf("loaded hit profile=%+v", loaded)
		}
	})

	t.Run("stale", func(t *testing.T) {
		user := newRecommendationProfileControllerIntegrationUser(t, db, "loader-stale")
		profile := compatibleControllerIntegrationProfile(user.ID, cfg, now.Add(-time.Minute), now.Add(-time.Hour))
		if err := db.Create(&profile).Error; err != nil {
			t.Fatal(err)
		}
		loaded, err := loadMaterializedUserInterestProfile(user.ID, now, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ProfileStatus != recommendationProfileStatusStale || !loaded.MaterializedInteractionsReady || len(loaded.PositiveVector) != 2 || len(loaded.NegativeVector) != 2 {
			t.Fatalf("loaded stale profile=%+v", loaded)
		}
		var dirty models.UserRecoProfileDirty
		if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("miss", func(t *testing.T) {
		user := newRecommendationProfileControllerIntegrationUser(t, db, "loader-miss")
		loaded, err := loadMaterializedUserInterestProfile(user.ID, now, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ProfileStatus != recommendationProfileStatusMiss || loaded.MaterializedInteractionsReady || len(loaded.PositiveVector) != 0 || len(loaded.NegativeVector) != 0 {
			t.Fatalf("loaded miss profile=%+v", loaded)
		}
		var dirty models.UserRecoProfileDirty
		if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
			t.Fatal(err)
		}
	})

	incompatibleCases := []struct {
		name   string
		mutate func(*models.UserRecoProfile)
	}{
		{name: "profile-version", mutate: func(profile *models.UserRecoProfile) { profile.ProfileVersion = "old-profile-version" }},
		{name: "profile-config", mutate: func(profile *models.UserRecoProfile) { profile.ProfileConfigHash = "wrong-config-hash" }},
		{name: "embedding-version", mutate: func(profile *models.UserRecoProfile) { profile.EmbeddingVersion = "old-embedding-version" }},
	}
	for _, test := range incompatibleCases {
		t.Run("incompatible-"+test.name, func(t *testing.T) {
			user := newRecommendationProfileControllerIntegrationUser(t, db, "loader-"+test.name)
			profile := compatibleControllerIntegrationProfile(user.ID, cfg, now.Add(time.Hour), now.Add(-time.Minute))
			test.mutate(&profile)
			if err := db.Create(&profile).Error; err != nil {
				t.Fatal(err)
			}
			loaded, err := loadMaterializedUserInterestProfile(user.ID, now, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.ProfileStatus != recommendationProfileStatusIncompatible || loaded.MaterializedInteractionsReady || len(loaded.PositiveVector) != 0 || len(loaded.NegativeVector) != 0 {
				t.Fatalf("loaded incompatible profile=%+v", loaded)
			}
			var dirty models.UserRecoProfileDirty
			if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializedInteractionExclusionIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	viewer := newRecommendationProfileControllerIntegrationUser(t, db, "interaction-viewer")
	author := newRecommendationProfileControllerIntegrationUser(t, db, "interaction-author")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	interacted := newRecommendationProfileControllerIntegrationArticle(t, db, author.ID, "interacted", now)
	eligible := newRecommendationProfileControllerIntegrationArticle(t, db, author.ID, "eligible", now.Add(-time.Minute))
	if err := db.Create(&models.UserPostRecoState{
		UserID: viewer.ID, PostID: interacted.ID, Interacted: true,
		CanonicalVersion: recommendation.CanonicalOutcomeVersion, RebuiltAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var stateCount int64
	if err := db.Model(&models.UserPostRecoState{}).Where("user_id = ? AND post_id = ?", viewer.ID, interacted.ID).Count(&stateCount).Error; err != nil {
		t.Fatal(err)
	}
	if stateCount != 1 {
		t.Fatal("materialized interaction fixture was not persisted")
	}

	profile := userInterestProfile{MaterializedInteractionsReady: true}
	candidates, err := loadRecommendationSourceCandidates(
		viewer.ID, profile, map[uint]servedPost{}, now, normalizedRecommendationConfig(), false,
		effectivePublishedAtSQL("posts", "pa_recommendation")+" DESC, posts.id DESC", 10, "recent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].PostID != eligible.ID {
		t.Fatalf("candidates=%+v want only eligible article=%d", candidates, eligible.ID)
	}
}

func TestImmediateNotInterestedProtectionBeforeMaterializerRefreshIntegration(t *testing.T) {
	db := openRecommendationProfileControllerIntegrationDB(t)
	viewer := newRecommendationProfileControllerIntegrationUser(t, db, "ni-viewer")
	author := newRecommendationProfileControllerIntegrationUser(t, db, "ni-author")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	article := newRecommendationProfileControllerIntegrationArticle(t, db, author.ID, "not-interested", now)
	if err := db.Create(&models.PostBehavior{
		UserID: viewer.ID, PostID: article.ID, Action: eventing.RecommendationBehaviorActionNotInterested,
		Count: 1, LastSeenAt: now.Add(-time.Minute), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var stateCount int64
	if err := db.Model(&models.UserPostRecoState{}).Where("user_id = ? AND post_id = ?", viewer.ID, article.ID).Count(&stateCount).Error; err != nil {
		t.Fatal(err)
	}
	if stateCount != 0 {
		t.Fatal("test must leave canonical interaction state missing")
	}

	candidates, err := loadRecommendationSourceCandidates(
		viewer.ID, userInterestProfile{MaterializedInteractionsReady: true}, map[uint]servedPost{}, now,
		normalizedRecommendationConfig(), false, effectivePublishedAtSQL("posts", "pa_recommendation")+" DESC, posts.id DESC", 10, "recent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("not-interested article was recalled before materializer refresh: %+v", candidates)
	}
}
