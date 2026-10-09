package tasks

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
)

func newLikeStateMaintenanceFixture(t *testing.T, count, version int64) (*likeStateClosureIntegration, []uint) {
	t.Helper()
	env := openLikeStateClosureIntegration(t)
	env.author = models.User{Username: "like-maintenance-author-" + strconv.FormatInt(time.Now().UnixNano(), 10), Password: "test"}
	env.actor = models.User{Username: "like-maintenance-actor-" + strconv.FormatInt(time.Now().UnixNano()+1, 10), Password: "test"}
	env.actor2 = models.User{Username: "like-maintenance-actor-two-" + strconv.FormatInt(time.Now().UnixNano()+2, 10), Password: "test"}
	if err := env.db.Create(&[]*models.User{&env.author, &env.actor, &env.actor2}).Error; err != nil {
		t.Fatal(err)
	}
	env.users = []uint{env.author.ID, env.actor.ID, env.actor2.ID}
	env.post = models.Post{
		AuthorID:        env.author.ID,
		Content:         "like state maintenance fixture",
		Visibility:      "public",
		LikeCount:       count,
		LikeSyncVersion: version,
	}
	if err := env.db.Create(&env.post).Error; err != nil {
		t.Fatal(err)
	}
	env.posts = []uint{env.post.ID}
	return env, env.users
}

func configureLikeStateMaintenanceExpiry(t *testing.T) {
	t.Helper()
	t.Setenv("LIKE_STATE_EXPIRY_ENABLED", "true")
	t.Setenv("LIKE_STATE_IDLE_BEFORE_EXPIRY", "1h")
	t.Setenv("LIKE_STATE_TTL", "1h")
}

func prepareLikeStateMaintenanceCandidate(t *testing.T, env *likeStateClosureIntegration, count, version int64, userIDs []uint, now time.Time) {
	t.Helper()
	if initialized, err := initializeLikeStore(env.store, t.Context(), env.post.ID, count, version, userIDs); err != nil || !initialized {
		t.Fatalf("initialize created=%t err=%v", initialized, err)
	}
	if err := env.store.TouchExpiryCandidate(t.Context(), env.post.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func addLikeStateMaintenanceReaction(t *testing.T, env *likeStateClosureIntegration, userID uint, liked bool, version int64) {
	t.Helper()
	at := time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC)
	if err := env.db.Create(&models.PostReaction{
		UserID: userID, PostID: env.post.ID, Reaction: models.PostReactionLike,
		Liked: liked, Version: version, UpdatedAt: at, StateChangedAt: at,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func assertLikeStateMaintenanceNotExpired(t *testing.T, env *likeStateClosureIntegration) {
	t.Helper()
	postIDString := strconv.FormatUint(uint64(env.post.ID), 10)
	if ttl, err := env.redis.TTL(likes.ReadyKey(env.post.ID)).Result(); err != nil || ttl != -1 {
		t.Fatalf("Ready ttl=%s err=%v want persistent", ttl, err)
	}
	if _, err := env.redis.HGet(likes.RecoverableVersionsKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("recovery marker err=%v want absent", err)
	}
	if _, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != nil {
		t.Fatalf("expiry candidate err=%v want retained", err)
	}
}

func assertLikeStateExpiryRejected(t *testing.T, env *likeStateClosureIntegration, now time.Time) {
	t.Helper()
	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); !errors.Is(err, likes.ErrLikeStateExpiryUnsupported) {
		t.Fatalf("expiry error=%v want explicit SPEC-02 boundary", err)
	}
}

func TestLikeStateMaintenanceRejectsExpiryUntilSPEC02Integration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 10)
	if _, err := runLikeStateMaintenancePass(t.Context(), env.store, env.db, 0, time.Now().UTC()); !errors.Is(err, likes.ErrLikeStateExpiryUnsupported) {
		t.Fatalf("maintenance error=%v want explicit unsupported", err)
	}
}

func TestLikeStateMaintenanceRejectsExpiryWithIncompletePostReactionProjectionIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 2)
	addLikeStateMaintenanceReaction(t, env, users[0], true, 1)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}

func TestLikeStateMaintenanceRejectsExpiryWithPostCountMismatchIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 2)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}

func TestLikeStateMaintenanceRejectsExpiryWithPostVersionMismatchIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 10)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}

func TestLikeStateMaintenanceRejectsExpiryWithoutPostCardinalityCheckIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 2)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}

func TestLikeStateMaintenanceRejectsExpiryBeforeQueueInspectionIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 10)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}

func TestLikeStateMaintenanceRejectsZeroStateExpiryIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 0, 0)
	assertLikeStateExpiryRejected(t, env, time.Now().UTC())
}
func TestLikeStateMaintenanceDisabledSkipsExpiryAndReconcilesRegistryIntegration(t *testing.T) {
	t.Setenv("LIKE_STATE_EXPIRY_ENABLED", "false")
	env, _ := newLikeStateMaintenanceFixture(t, 0, 0)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 0, 0, nil, now)
	postIDString := strconv.FormatUint(uint64(env.post.ID), 10)
	initialScore, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result()
	if err != nil {
		t.Fatal(err)
	}

	deletedPost := models.Post{AuthorID: env.author.ID, Content: "deleted registry fixture", Visibility: "public"}
	if err := env.db.Create(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	env.posts = append(env.posts, deletedPost.ID)
	if err := env.db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	if initialized, err := initializeLikeStore(env.store, t.Context(), deletedPost.ID, 0, 0, nil); err != nil || !initialized {
		t.Fatalf("initialize deleted state created=%t err=%v", initialized, err)
	}

	if _, err := runLikeStateMaintenancePass(t.Context(), env.store, env.db, 0, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceNotExpired(t, env)
	if score, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != nil || score != initialScore {
		t.Fatalf("expiry candidate score=%v err=%v want unchanged score %v", score, err, initialScore)
	}
	if registered, err := env.redis.SIsMember(likes.RegistryKey, deletedPost.ID).Result(); err != nil || registered {
		t.Fatalf("deleted post registered=%t err=%v want reconciled", registered, err)
	}
}
