package tasks

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/embeddingstate"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openPostEmbeddingIntegrationDatabase(t *testing.T) *gorm.DB {
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
		&models.User{}, &models.Post{}, &models.PostEmbedding{},
		&models.PostBehavior{}, &models.PostReaction{}, &models.UserRecoProfileDirty{},
	); err != nil {
		t.Fatal(err)
	}
	originalDB, originalWorkerDB := global.Db, global.WorkerDb
	global.Db = db
	global.WorkerDb = db
	t.Cleanup(func() { global.Db, global.WorkerDb = originalDB, originalWorkerDB })
	if err := initialize.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	previousServingVersion, err := embeddingstate.LoadServingVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := embeddingstate.SetServingVersion(context.Background(), db, "v1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := embeddingstate.SetServingVersion(context.Background(), db, previousServingVersion); err != nil {
			t.Errorf("restore embedding serving version: %v", err)
		}
	})
	return db
}

func newPostEmbeddingIntegrationFixture(t *testing.T, db *gorm.DB, content string) (models.User, models.Post) {
	t.Helper()
	user := models.User{Username: "embedding-owner-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{AuthorID: user.ID, Content: content, Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	return user, article
}

func postgresTimestampEqual(got, want time.Time) bool {
	delta := got.Sub(want)
	return delta > -time.Microsecond && delta < time.Microsecond
}

func TestPostEmbeddingGORMStoreIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)

	user := models.User{Username: "embedding-owner-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{AuthorID: user.ID, Content: "Body", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	store := gormPostEmbeddingStore{db: db}
	loadedPost, err := store.GetPost(context.Background(), article.ID)
	if err != nil || loadedPost.ID != article.ID {
		t.Fatalf("post=%#v err=%v", loadedPost, err)
	}
	if _, err := store.GetEmbedding(context.Background(), article.ID, "v1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing embedding err=%v", err)
	}

	first := models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test-model",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 2}),
		ContentHash: embeddings.PostEmbeddingContentHash(article.Content), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	first.CreatedAt = time.Now().UTC().Add(-time.Hour)
	first.UpdatedAt = first.CreatedAt
	outcome, err := store.CommitEmbeddingIfCurrent(context.Background(), first, time.Now().UTC())
	if err != nil || outcome != postEmbeddingWriteCommitted {
		t.Fatalf("first outcome=%d err=%v", outcome, err)
	}
	second := first
	second.Version = "v2"
	second.ContentHash = embeddings.PostEmbeddingContentHash(article.Content)
	second.Embedding = pgvector.NewVector([]float32{3, 4})
	second.UpdatedAt = time.Now().UTC()
	outcome, err = store.CommitEmbeddingIfCurrent(context.Background(), second, time.Now().UTC())
	if err != nil || outcome != postEmbeddingWriteCommitted {
		t.Fatalf("second outcome=%d err=%v", outcome, err)
	}
	persisted, err := store.GetEmbedding(context.Background(), article.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != "v2" || persisted.ContentHash != embeddings.PostEmbeddingContentHash(article.Content) || len(persisted.Embedding.Slice()) != 2 {
		t.Fatalf("embedding=%#v", persisted)
	}
	if !postgresTimestampEqual(persisted.CreatedAt, first.CreatedAt) {
		t.Fatalf("new v2 row created_at=%s want=%s", persisted.CreatedAt, first.CreatedAt)
	}
	updatedV2 := second
	updatedV2.Model = "updated-model"
	updatedV2.Embedding = pgvector.NewVector([]float32{5, 6})
	updatedV2.CreatedAt = time.Now().UTC()
	updatedV2.UpdatedAt = time.Now().UTC().Add(time.Minute)
	if outcome, err = store.CommitEmbeddingIfCurrent(context.Background(), updatedV2, time.Now().UTC()); err != nil || outcome != postEmbeddingWriteCommitted {
		t.Fatalf("updated v2 outcome=%d err=%v", outcome, err)
	}
	persisted, err = store.GetEmbedding(context.Background(), article.ID, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Model != "updated-model" || persisted.Embedding.Slice()[0] != 5 || !postgresTimestampEqual(persisted.CreatedAt, first.CreatedAt) || !postgresTimestampEqual(persisted.UpdatedAt, updatedV2.UpdatedAt) {
		t.Fatalf("updated v2 embedding=%+v", persisted)
	}
	persistedV1, err := store.GetEmbedding(context.Background(), article.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if persistedV1.Model != first.Model || persistedV1.Embedding.Slice()[0] != 1 || !postgresTimestampEqual(persistedV1.CreatedAt, first.CreatedAt) {
		t.Fatalf("v1 embedding changed after v2 writes: %+v", persistedV1)
	}
	var count int64
	if err := db.Model(&models.PostEmbedding{}).Where("post_id = ?", article.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("rows=%d want=2", count)
	}
}

func TestCommitEmbeddingIfCurrentRejectsChangedContentIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	user, article := newPostEmbeddingIntegrationFixture(t, db, "H1")
	now := time.Now().UTC()
	if err := db.Create(&models.PostBehavior{
		UserID: user.ID, PostID: article.ID, Action: recommendation.PostBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Update("content", "H2").Error; err != nil {
		t.Fatal(err)
	}

	embedding := models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test-model", Dimensions: 2,
		Embedding:   pgvector.NewVector([]float32{1, 2}),
		ContentHash: embeddings.PostEmbeddingContentHash("H1"), CreatedAt: now, UpdatedAt: now,
	}
	outcome, err := (gormPostEmbeddingStore{db: db}).CommitEmbeddingIfCurrent(context.Background(), embedding, now)
	if err != nil || outcome != postEmbeddingWriteStaleContent {
		t.Fatalf("outcome=%d err=%v", outcome, err)
	}
	if _, err := (gormPostEmbeddingStore{db: db}).GetEmbedding(context.Background(), article.ID, "v1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale embedding lookup err=%v", err)
	}
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 0 {
		t.Fatalf("stale write invalidated %d profiles", dirtyCount)
	}
}

func TestCommitEmbeddingIfCurrentPreservesNewerEmbeddingIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	_, article := newPostEmbeddingIntegrationFixture(t, db, "H2")
	now := time.Now().UTC()
	existing := models.PostEmbedding{
		PostID: article.ID, Version: "v2", Model: "newer-model", Dimensions: 2,
		Embedding:   pgvector.NewVector([]float32{3, 4}),
		ContentHash: embeddings.PostEmbeddingContentHash("H2"), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	var before models.PostEmbedding
	if err := db.First(&before, "post_id = ? AND version = ?", article.ID, "v2").Error; err != nil {
		t.Fatal(err)
	}
	stale := existing
	stale.Version = "v1"
	stale.Model = "stale-model"
	stale.ContentHash = embeddings.PostEmbeddingContentHash("H1")
	stale.Embedding = pgvector.NewVector([]float32{1, 2})

	outcome, err := (gormPostEmbeddingStore{db: db}).CommitEmbeddingIfCurrent(context.Background(), stale, now.Add(time.Minute))
	if err != nil || outcome != postEmbeddingWriteStaleContent {
		t.Fatalf("outcome=%d err=%v", outcome, err)
	}
	var persisted models.PostEmbedding
	if err := db.First(&persisted, "post_id = ? AND version = ?", article.ID, "v2").Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Version != before.Version || persisted.Model != before.Model || persisted.Dimensions != before.Dimensions ||
		persisted.ContentHash != before.ContentHash || persisted.Embedding.Slice()[0] != before.Embedding.Slice()[0] ||
		persisted.Embedding.Slice()[1] != before.Embedding.Slice()[1] || !persisted.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("newer embedding was overwritten: before=%+v persisted=%+v", before, persisted)
	}
}

func TestCommitEmbeddingIfCurrentWaitsForPostRowLockIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	_, article := newPostEmbeddingIntegrationFixture(t, db, "H1")
	now := time.Now().UTC()
	embedding := models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test-model", Dimensions: 2,
		Embedding:   pgvector.NewVector([]float32{1, 2}),
		ContentHash: embeddings.PostEmbeddingContentHash("H1"), CreatedAt: now, UpdatedAt: now,
	}

	dbConn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	dbConn.SetMaxOpenConns(4)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err := tx.Model(&models.Post{}).Where("id = ?", article.ID).Update("content", "H2").Error; err != nil {
		tx.Rollback()
		t.Fatal(err)
	}

	type writeResult struct {
		outcome postEmbeddingWriteOutcome
		err     error
	}
	resultCh := make(chan writeResult, 1)
	go func() {
		outcome, err := (gormPostEmbeddingStore{db: db}).CommitEmbeddingIfCurrent(context.Background(), embedding, now)
		resultCh <- writeResult{outcome: outcome, err: err}
	}()
	select {
	case result := <-resultCh:
		tx.Rollback()
		t.Fatalf("conditional write completed before source transaction committed: outcome=%d err=%v", result.outcome, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil || result.outcome != postEmbeddingWriteStaleContent {
			t.Fatalf("outcome=%d err=%v", result.outcome, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("conditional write did not finish after source transaction committed")
	}
	if _, err := (gormPostEmbeddingStore{db: db}).GetEmbedding(context.Background(), article.ID, "v1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale embedding lookup err=%v", err)
	}
}

func TestEmbeddingWriteServingStateLockOrdersRuntimeCutoverIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	_, article := newPostEmbeddingIntegrationFixture(t, db, "H1")
	dbConn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	dbConn.SetMaxOpenConns(4)

	writeTx := db.Begin()
	if writeTx.Error != nil {
		t.Fatal(writeTx.Error)
	}
	defer writeTx.Rollback()
	servingVersion, err := lockEmbeddingServingVersion(writeTx)
	if err != nil {
		t.Fatal(err)
	}
	if servingVersion != "v1" {
		t.Fatalf("locked serving version=%q want v1", servingVersion)
	}
	now := time.Now().UTC()
	if err := writeTx.Create(&models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test-model", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 2}), ContentHash: embeddings.PostEmbeddingContentHash(article.Content),
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	setterDone := make(chan error, 1)
	go func() {
		setterDone <- embeddingstate.SetServingVersion(context.Background(), db, "v2")
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	setterWaitingForLock := false
	for !setterWaitingForLock {
		var blocked bool
		if err := db.Raw(`
SELECT EXISTS (
	SELECT 1
	FROM pg_stat_activity
	WHERE datname = current_database()
	  AND pid <> pg_backend_pid()
	  AND wait_event_type = 'Lock'
	  AND query ILIKE '%UPDATE embedding_serving_state%'
)`).Scan(&blocked).Error; err != nil {
			t.Fatal(err)
		}
		if blocked {
			setterWaitingForLock = true
			break
		}
		select {
		case setterErr := <-setterDone:
			t.Fatalf("serving-state setter completed before write transaction released its lock: %v", setterErr)
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("serving-state setter never appeared waiting on the write transaction lock")
		}
	}

	if got, err := embeddingstate.LoadServingVersion(context.Background(), db); err != nil || got != "v1" {
		t.Fatalf("serving version while write lock held=%q err=%v, want v1", got, err)
	}
	if err := writeTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-setterDone:
		if err != nil {
			t.Fatalf("serving-state setter after write commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serving-state setter did not complete after write transaction committed")
	}
	if got, err := embeddingstate.LoadServingVersion(context.Background(), db); err != nil || got != "v2" {
		t.Fatalf("serving version after lock release=%q err=%v, want v2", got, err)
	}
}
