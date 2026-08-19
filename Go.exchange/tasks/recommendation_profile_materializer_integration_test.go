package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const recommendationProfileIntegrationEmbeddingVersion = "p1a-follow-up-embedding-v1"

func openRecommendationProfileMaterializerIntegrationDB(t *testing.T) *gorm.DB {
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
		Embedding: config.EmbeddingConfig{Version: recommendationProfileIntegrationEmbeddingVersion},
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

func newRecommendationProfileIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{Username: "p1a-follow-up-" + label + "-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func newRecommendationProfileIntegrationArticle(t *testing.T, db *gorm.DB, authorID uint, title string, publishedAt time.Time) models.Article {
	t.Helper()
	article := models.Article{
		AuthorID:         authorID,
		Title:            title,
		Content:          title + " body",
		Preview:          title + " preview",
		PublicationState: "published",
		PublishedAt:      &publishedAt,
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func cleanupRecommendationProfileIntegrationData(db *gorm.DB, articleIDs, userIDs []uint) {
	if db == nil {
		return
	}
	if len(articleIDs) > 0 {
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleEmbedding{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.UserArticleRecoState{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("article_id IN ?", articleIDs).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
	}
	if len(userIDs) > 0 {
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfile{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserArticleRecoState{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserAuthorAffinity{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func recommendationProfileIntegrationSettings() config.RecommendationProfileMaterializationConfig {
	return config.RecommendationProfileMaterializationConfig{
		DebounceSeconds:          1,
		PollIntervalSeconds:      1,
		BatchSize:                50,
		RebuildIntervalHours:     2,
		StaleScanIntervalSeconds: 60,
		StaleEnqueueBatchSize:    50,
	}.Normalized()
}

func newRecommendationProfileIntegrationEmbedding(articleID uint, version string, values []float32, hash string, now time.Time) models.ArticleEmbedding {
	return models.ArticleEmbedding{
		ArticleID:   articleID,
		Version:     version,
		Model:       "p1a-follow-up-test-model",
		Dimensions:  len(values),
		Embedding:   pgvector.NewVector(values),
		ContentHash: hash,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestRecommendationProfileMaterializerIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	user := newRecommendationProfileIntegrationUser(t, db, "materializer-user")
	author := newRecommendationProfileIntegrationUser(t, db, "materializer-author")
	positiveArticle := newRecommendationProfileIntegrationArticle(t, db, author.ID, "positive", now.Add(-2*time.Hour))
	negativeArticle := newRecommendationProfileIntegrationArticle(t, db, author.ID, "negative", now.Add(-time.Hour))
	articleIDs := []uint{positiveArticle.ID, negativeArticle.ID}
	userIDs := []uint{user.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationProfileIntegrationData(db, articleIDs, userIDs) })

	if err := db.Create(&models.ArticleReaction{
		UserID: user.ID, ArticleID: positiveArticle.ID, Reaction: models.ArticleReactionLike, Liked: true,
		Version: 1, StateChangedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleBehavior{
		UserID: user.ID, ArticleID: negativeArticle.ID, Action: eventing.RecommendationBehaviorActionReadQuickBounce,
		Count: 1, LastSeenAt: now.Add(-30 * time.Minute), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	version := config.ActiveEmbeddingVersion()
	for _, embedding := range []models.ArticleEmbedding{
		newRecommendationProfileIntegrationEmbedding(positiveArticle.ID, version, []float32{1, 0}, "positive", now),
		newRecommendationProfileIntegrationEmbedding(negativeArticle.ID, version, []float32{0, 1}, "negative", now),
	} {
		if err := db.Create(&embedding).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "integration_source_change", now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	settings := recommendationProfileIntegrationSettings()
	cutoff := now.Add(-time.Minute)
	if err := materializeRecommendationProfileUser(user.ID, now, settings, cutoff); err != nil {
		t.Fatal(err)
	}

	var profile models.UserRecoProfile
	if err := db.First(&profile, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if profile.ProfileVersion != recommendation.MaterializedProfileVersion {
		t.Fatalf("profile version=%q", profile.ProfileVersion)
	}
	if profile.ProfileConfigHash == "" {
		t.Fatal("profile config hash is empty")
	}
	if profile.EmbeddingVersion != version {
		t.Fatalf("embedding version=%q want=%q", profile.EmbeddingVersion, version)
	}
	if profile.ComputedAt.IsZero() || !profile.NextRebuildAt.After(profile.ComputedAt) {
		t.Fatalf("computed_at=%s next_rebuild_at=%s", profile.ComputedAt, profile.NextRebuildAt)
	}
	if profile.Dimensions != 2 || profile.PositiveVector == nil || profile.NegativeVector == nil {
		t.Fatalf("profile vectors/dimensions=%d positive=%v negative=%v", profile.Dimensions, profile.PositiveVector != nil, profile.NegativeVector != nil)
	}
	if profile.NegativeEvidence <= 0 || profile.PositiveSignalCount != 1 || profile.NegativeSignalCount != 1 || profile.PersonalizedSignalCount != 2 {
		t.Fatalf("profile signal values=%+v", profile)
	}

	var states []models.UserArticleRecoState
	if err := db.Where("user_id = ?", user.ID).Order("article_id ASC").Find(&states).Error; err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].ArticleID != positiveArticle.ID || states[1].ArticleID != negativeArticle.ID {
		t.Fatalf("canonical states=%+v", states)
	}
	if states[1].NegativeSignal != "quick_bounce" {
		t.Fatalf("negative canonical state=%+v", states[1])
	}

	var affinities []models.UserAuthorAffinity
	if err := db.Where("user_id = ?", user.ID).Find(&affinities).Error; err != nil {
		t.Fatal(err)
	}
	if len(affinities) != 1 || affinities[0].AuthorID != author.ID || affinities[0].RawAffinity <= 0 {
		t.Fatalf("author affinities=%+v", affinities)
	}
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 0 {
		t.Fatalf("dirty rows=%d want=0 after successful materialization", dirtyCount)
	}
}

func TestRecommendationProfileDirtyVersionDeleteRaceIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user := newRecommendationProfileIntegrationUser(t, db, "delete-race")
	t.Cleanup(func() { cleanupRecommendationProfileIntegrationData(db, nil, []uint{user.ID}) })
	first := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "first", first); err != nil {
		t.Fatal(err)
	}
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "newer", second); err != nil {
		t.Fatal(err)
	}
	rows, err := deleteMaterializedProfileClaim(db, user.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("stale cleanup deleted %d rows", rows)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != 2 || dirty.Reason != "newer" {
		t.Fatalf("dirty row after stale cleanup=%+v", dirty)
	}
}

func TestRecommendationProfileRetryRaceIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user := newRecommendationProfileIntegrationUser(t, db, "retry-race")
	t.Cleanup(func() { cleanupRecommendationProfileIntegrationData(db, nil, []uint{user.ID}) })
	first := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "first", first); err != nil {
		t.Fatal(err)
	}
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "newer", second); err != nil {
		t.Fatal(err)
	}
	var before models.UserRecoProfileDirty
	if err := db.First(&before, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if before.DirtyVersion != 2 || before.Attempts != 0 || before.LastError != "" {
		t.Fatalf("new dirty generation before stale retry=%+v", before)
	}
	if err := retryMaterializedProfileClaim(
		recommendationProfileMaterializerClaim{UserID: user.ID, DirtyVersion: 1, Attempts: 0},
		errors.New("stale failure"),
		second.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var after models.UserRecoProfileDirty
	if err := db.First(&after, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.DirtyVersion != before.DirtyVersion || after.Attempts != before.Attempts || after.LastError != before.LastError || !after.NextAttemptAt.Equal(before.NextAttemptAt) {
		t.Fatalf("stale retry changed newer generation: before=%+v after=%+v", before, after)
	}
}

func TestRecommendationProfileQueueSemanticsIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user := newRecommendationProfileIntegrationUser(t, db, "queue-semantics")
	t.Cleanup(func() { cleanupRecommendationProfileIntegrationData(db, nil, []uint{user.ID}) })
	first := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "source-change", first); err != nil {
		t.Fatal(err)
	}
	if err := recommendation.EnsureProfilesQueued(db, []uint{user.ID}, "serving-stale", second); err != nil {
		t.Fatal(err)
	}
	var queued models.UserRecoProfileDirty
	if err := db.First(&queued, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if queued.DirtyVersion != 1 || queued.Reason != "source-change" || queued.Attempts != 0 || queued.LastError != "" || !queued.NextAttemptAt.Equal(first) {
		t.Fatalf("EnsureProfilesQueued changed an existing generation: %+v", queued)
	}
	if err := recommendation.InvalidateProfiles(db, []uint{user.ID}, "new-source-change", second); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&queued, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if queued.DirtyVersion != 2 || queued.Reason != "new-source-change" || queued.Attempts != 0 || queued.LastError != "" || !queued.NextAttemptAt.Equal(second) {
		t.Fatalf("InvalidateProfiles did not advance/reset the dirty generation: %+v", queued)
	}
}

func newArticleEmbeddingFanoutFixture(t *testing.T, db *gorm.DB, label string) (models.User, models.Article) {
	t.Helper()
	user := newRecommendationProfileIntegrationUser(t, db, "embedding-"+label+"-user")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	article := newRecommendationProfileIntegrationArticle(t, db, user.ID, "embedding-"+label, now)
	t.Cleanup(func() { cleanupRecommendationProfileIntegrationData(db, []uint{article.ID}, []uint{user.ID}) })
	return user, article
}

func TestArticleEmbeddingUpdateInvalidatesAffectedProfileIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "success")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ArticleBehavior{
		UserID: user.ID, ArticleID: article.ID, Action: recommendation.ArticleBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store := gormArticleEmbeddingStore{db: db}
	embedding := newRecommendationProfileIntegrationEmbedding(article.ID, config.ActiveEmbeddingVersion(), []float32{1, 0}, "success", now)
	if err := store.UpsertEmbeddingAndInvalidateProfiles(context.Background(), embedding, now); err != nil {
		t.Fatal(err)
	}
	var persisted models.ArticleEmbedding
	if err := db.First(&persisted, "article_id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ContentHash != "success" {
		t.Fatalf("embedding=%+v", persisted)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
}

func TestArticleEmbeddingFanoutWorksBeforeCanonicalStateIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "before-canonical")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ArticleBehavior{
		UserID: user.ID, ArticleID: article.ID, Action: recommendation.ArticleBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var stateCount int64
	if err := db.Model(&models.UserArticleRecoState{}).Where("user_id = ?", user.ID).Count(&stateCount).Error; err != nil {
		t.Fatal(err)
	}
	if stateCount != 0 {
		t.Fatalf("canonical state unexpectedly exists before fan-out: %d", stateCount)
	}
	store := gormArticleEmbeddingStore{db: db}
	embedding := newRecommendationProfileIntegrationEmbedding(article.ID, config.ActiveEmbeddingVersion(), []float32{1, 0}, "before-canonical", now)
	if err := store.UpsertEmbeddingAndInvalidateProfiles(context.Background(), embedding, now); err != nil {
		t.Fatal(err)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
}

func TestArticleEmbeddingFanoutIncludesReactionOnlyUserIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "reaction-only")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ArticleReaction{
		UserID: user.ID, ArticleID: article.ID, Reaction: models.ArticleReactionLike, Liked: false,
		Version: 1, StateChangedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store := gormArticleEmbeddingStore{db: db}
	embedding := newRecommendationProfileIntegrationEmbedding(article.ID, config.ActiveEmbeddingVersion(), []float32{1, 0}, "reaction-only", now)
	if err := store.UpsertEmbeddingAndInvalidateProfiles(context.Background(), embedding, now); err != nil {
		t.Fatal(err)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
}

func TestArticleEmbeddingFanoutDeduplicatesBehaviorAndReactionUserIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "duplicate-user")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ArticleBehavior{
		UserID: user.ID, ArticleID: article.ID, Action: recommendation.ArticleBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleReaction{
		UserID: user.ID, ArticleID: article.ID, Reaction: models.ArticleReactionLike, Liked: true,
		Version: 1, StateChangedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store := gormArticleEmbeddingStore{db: db}
	embedding := newRecommendationProfileIntegrationEmbedding(article.ID, config.ActiveEmbeddingVersion(), []float32{1, 0}, "duplicate-user", now)
	if err := store.UpsertEmbeddingAndInvalidateProfiles(context.Background(), embedding, now); err != nil {
		t.Fatal(err)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.First(&dirty, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion != 1 {
		t.Fatalf("dirty version=%d want=1; fan-out likely invalidated twice", dirty.DirtyVersion)
	}
}

func TestArticleEmbeddingUpdateRollsBackWhenProfileInvalidationFailsIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "rollback")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&models.ArticleBehavior{
		UserID: user.ID, ArticleID: article.ID, Action: recommendation.ArticleBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store := gormArticleEmbeddingStore{db: db}
	old := newRecommendationProfileIntegrationEmbedding(article.ID, "old-version", []float32{1, 0}, "old", now)
	if err := store.UpsertEmbedding(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	const constraintName = "chk_p1a_follow_up_invalidation_failure"
	if err := db.Exec("ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS " + constraintName).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec("ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS " + constraintName) })
	if err := db.Exec("ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT " + constraintName + " CHECK (reason <> 'article_embedding_changed')").Error; err != nil {
		t.Fatal(err)
	}

	updated := newRecommendationProfileIntegrationEmbedding(article.ID, "new-version", []float32{0, 1}, "new", now.Add(time.Minute))
	if err := store.UpsertEmbeddingAndInvalidateProfiles(context.Background(), updated, now.Add(time.Minute)); err == nil {
		t.Fatal("embedding update unexpectedly succeeded when invalidation constraint rejected the dirty row")
	}
	if err := db.Exec("ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS " + constraintName).Error; err != nil {
		t.Fatal(err)
	}

	var persisted models.ArticleEmbedding
	if err := db.First(&persisted, "article_id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Version != old.Version || persisted.ContentHash != old.ContentHash || persisted.Embedding.Slice()[0] != 1 {
		t.Fatalf("embedding changed despite rollback: %+v", persisted)
	}
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 0 {
		t.Fatalf("dirty rows=%d after rolled back invalidation", dirtyCount)
	}
}

type unchangedArticleEmbeddingProvider struct{}

func (unchangedArticleEmbeddingProvider) Embed(context.Context, []string) (embeddings.EmbedResult, error) {
	return embeddings.EmbedResult{Vectors: [][]float32{{1, 0}}, Model: "unchanged-provider"}, nil
}

func TestArticleEmbeddingUpToDateDoesNotInvalidateProfileIntegration(t *testing.T) {
	db := openRecommendationProfileMaterializerIntegrationDB(t)
	user, article := newArticleEmbeddingFanoutFixture(t, db, "up-to-date")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	version := config.ActiveEmbeddingVersion()
	hash := embeddings.ArticleEmbeddingContentHash(article.Title, article.Content)
	if err := db.Create(&models.ArticleEmbedding{
		ArticleID: article.ID, Version: version, Model: "existing-model", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: hash, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	event, err := eventing.NewArticleEmbeddingRequestedEnvelope(uuid.NewString(), article.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	store := gormArticleEmbeddingStore{db: db}
	if err := processArticleEmbeddingMessage(context.Background(), kafka.Message{Value: raw}, unchangedArticleEmbeddingProvider{}, store, version); err != nil {
		t.Fatal(err)
	}
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 0 {
		t.Fatalf("up-to-date embedding created %d dirty rows", dirtyCount)
	}
}
