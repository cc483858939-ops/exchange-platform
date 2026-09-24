package tasks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/embeddingstate"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

func TestPostEmbeddingProjectionRecoverySemanticsIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	store := gormPostEmbeddingStore{db: db}
	now := time.Now().UTC()

	t.Run("committed embedding invalidates affected profile", func(t *testing.T) {
		user, post := newPostEmbeddingIntegrationFixture(t, db, "content A")
		if err := db.Create(&models.PostBehavior{
			UserID: user.ID, PostID: post.ID, Action: recommendation.PostBehaviorView,
			Count: 1, LastSeenAt: now, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
		embedding := recoveryIntegrationEmbedding(post.ID, "v1", post.Content, []float32{1, 2}, now)
		outcome, err := store.CommitEmbeddingIfCurrent(context.Background(), embedding, now)
		if err != nil || outcome != postEmbeddingWriteCommitted {
			t.Fatalf("outcome=%d err=%v", outcome, err)
		}
		persisted, err := store.GetEmbedding(context.Background(), post.ID, "v1")
		if err != nil || persisted.Version != "v1" || persisted.ContentHash != embeddings.PostEmbeddingContentHash(post.Content) {
			t.Fatalf("embedding=%+v err=%v", persisted, err)
		}
		var dirtyCount int64
		if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
			t.Fatal(err)
		}
		if dirtyCount != 1 {
			t.Fatalf("dirty profile rows=%d want=1", dirtyCount)
		}
	})

	t.Run("shadow build does not invalidate serving profile", func(t *testing.T) {
		user, post := newPostEmbeddingIntegrationFixture(t, db, "shadow content")
		if err := db.Create(&models.PostBehavior{
			UserID: user.ID, PostID: post.ID, Action: recommendation.PostBehaviorView,
			Count: 1, LastSeenAt: now, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
		embedding := recoveryIntegrationEmbedding(post.ID, "post_embedding_v2", post.Content, []float32{2, 1}, now)
		outcome, err := store.CommitEmbeddingIfCurrent(context.Background(), embedding, now)
		if err != nil || outcome != postEmbeddingWriteCommitted {
			t.Fatalf("outcome=%d err=%v", outcome, err)
		}
		var dirtyCount int64
		if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
			t.Fatal(err)
		}
		if dirtyCount != 0 {
			t.Fatalf("shadow write invalidated %d serving profiles", dirtyCount)
		}
	})

	t.Run("changed source rejects stale embedding", func(t *testing.T) {
		_, post := newPostEmbeddingIntegrationFixture(t, db, "content A")
		embedding := recoveryIntegrationEmbedding(post.ID, "v1", post.Content, []float32{1, 2}, now)
		if err := db.Model(&models.Post{}).Where("id = ?", post.ID).Update("content", "content B").Error; err != nil {
			t.Fatal(err)
		}
		outcome, err := store.CommitEmbeddingIfCurrent(context.Background(), embedding, now)
		if err != nil || outcome != postEmbeddingWriteStaleContent {
			t.Fatalf("outcome=%d err=%v", outcome, err)
		}
		var embeddingCount int64
		if err := db.Model(&models.PostEmbedding{}).Where("post_id = ?", post.ID).Count(&embeddingCount).Error; err != nil {
			t.Fatal(err)
		}
		if embeddingCount != 0 {
			t.Fatalf("stale embedding rows=%d want=0", embeddingCount)
		}
	})

	t.Run("missing post has no write", func(t *testing.T) {
		_, post := newPostEmbeddingIntegrationFixture(t, db, "content A")
		if err := db.Delete(&models.Post{}, post.ID).Error; err != nil {
			t.Fatal(err)
		}
		embedding := recoveryIntegrationEmbedding(post.ID, "v1", post.Content, []float32{1, 2}, now)
		outcome, err := store.CommitEmbeddingIfCurrent(context.Background(), embedding, now)
		if err != nil || outcome != postEmbeddingWritePostMissing {
			t.Fatalf("outcome=%d err=%v", outcome, err)
		}
		var embeddingCount int64
		if err := db.Model(&models.PostEmbedding{}).Where("post_id = ?", post.ID).Count(&embeddingCount).Error; err != nil {
			t.Fatal(err)
		}
		if embeddingCount != 0 {
			t.Fatalf("missing-post embedding rows=%d want=0", embeddingCount)
		}
	})

	t.Run("profile invalidation failure rolls back embedding upsert", func(t *testing.T) {
		user, post := newPostEmbeddingIntegrationFixture(t, db, "content A")
		if err := db.Create(&models.PostBehavior{
			UserID: user.ID, PostID: post.ID, Action: recommendation.PostBehaviorView,
			Count: 1, LastSeenAt: now, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
		old := recoveryIntegrationEmbedding(post.ID, "old-version", post.Content, []float32{3, 4}, now)
		if err := db.Create(&old).Error; err != nil {
			t.Fatal(err)
		}
		constraintName := "chk_post_embedding_recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if err := db.Exec(fmt.Sprintf("ALTER TABLE user_reco_profile_dirty ADD CONSTRAINT %s CHECK (reason <> 'post_embedding_changed')", constraintName)).Error; err != nil {
			t.Fatal(err)
		}
		dropConstraint := func() error {
			return db.Exec("ALTER TABLE user_reco_profile_dirty DROP CONSTRAINT IF EXISTS " + constraintName).Error
		}
		t.Cleanup(func() {
			if err := dropConstraint(); err != nil {
				t.Errorf("drop test constraint: %v", err)
			}
		})

		updated := recoveryIntegrationEmbedding(post.ID, "v1", post.Content, []float32{1, 2}, now.Add(time.Minute))
		if _, err := store.CommitEmbeddingIfCurrent(context.Background(), updated, now.Add(time.Minute)); err == nil {
			t.Fatal("embedding update unexpectedly succeeded when profile invalidation was rejected")
		}
		if err := dropConstraint(); err != nil {
			t.Fatal(err)
		}
		persisted, err := store.GetEmbedding(context.Background(), post.ID, "old-version")
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Version != old.Version || persisted.Model != old.Model || persisted.ContentHash != old.ContentHash || persisted.Embedding.Slice()[0] != 3 || persisted.Embedding.Slice()[1] != 4 {
			t.Fatalf("embedding changed despite transaction rollback: %+v", persisted)
		}
		var dirtyCount int64
		if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
			t.Fatal(err)
		}
		if dirtyCount != 0 {
			t.Fatalf("dirty profile rows=%d after rollback, want=0", dirtyCount)
		}
	})
}

func TestPostEmbeddingCommitInvalidatesProfilesAfterRuntimeServingSwitchIntegration(t *testing.T) {
	db := openPostEmbeddingIntegrationDatabase(t)
	user, post := newPostEmbeddingIntegrationFixture(t, db, "v2 serving")
	now := time.Now().UTC()
	if err := db.Create(&models.PostBehavior{
		UserID: user.ID, PostID: post.ID, Action: recommendation.PostBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v2"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := embeddingstate.SetServingVersion(context.Background(), db, "v1"); err != nil {
			t.Errorf("restore integration serving version: %v", err)
		}
	})

	embedding := recoveryIntegrationEmbedding(post.ID, "post_embedding_v2", post.Content, []float32{2, 1}, now)
	outcome, err := (gormPostEmbeddingStore{db: db}).CommitEmbeddingIfCurrent(context.Background(), embedding, now)
	if err != nil || outcome != postEmbeddingWriteCommitted {
		t.Fatalf("outcome=%d err=%v", outcome, err)
	}
	if _, err := (gormPostEmbeddingStore{db: db}).GetEmbedding(context.Background(), post.ID, "post_embedding_v2"); err != nil {
		t.Fatalf("serving v2 embedding was not written: %v", err)
	}
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", user.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 1 {
		t.Fatalf("v2 serving write invalidated %d profiles, want 1", dirtyCount)
	}
}

func recoveryIntegrationEmbedding(postID uint, version, content string, vector []float32, now time.Time) models.PostEmbedding {
	return models.PostEmbedding{
		PostID: postID, Version: version, Model: "recovery-integration-model", Dimensions: len(vector),
		Embedding: pgvector.NewVector(vector), ContentHash: embeddings.PostEmbeddingContentHash(content), CreatedAt: now, UpdatedAt: now,
	}
}
