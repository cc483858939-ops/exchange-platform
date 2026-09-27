package tasks

import (
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
	if initialized, err := env.store.Initialize(t.Context(), env.post.ID, count, version, userIDs); err != nil || !initialized {
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

func assertLikeStateMaintenanceExpired(t *testing.T, env *likeStateClosureIntegration) {
	t.Helper()
	postIDString := strconv.FormatUint(uint64(env.post.ID), 10)
	if ttl, err := env.redis.TTL(likes.ReadyKey(env.post.ID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("Ready ttl=%s err=%v want armed", ttl, err)
	}
	if marker, err := env.redis.HGet(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker != strconv.FormatInt(env.post.LikeSyncVersion, 10) {
		t.Fatalf("recovery marker=%q err=%v want version %d", marker, err, env.post.LikeSyncVersion)
	}
	if _, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("expiry candidate err=%v want removed", err)
	}
}

func TestLikeStateMaintenanceAllowsExpiryWhenAggregateStateMatches(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 10)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 2, 10, []uint{users[0], users[1]}, now)
	addLikeStateMaintenanceReaction(t, env, users[0], true, 9)
	addLikeStateMaintenanceReaction(t, env, users[2], true, 10)

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceExpired(t, env)
}

func TestLikeStateMaintenanceAllowsExpiryWithIncompletePostReactionProjectionIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 2)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 2, 2, []uint{users[0], users[1]}, now)
	addLikeStateMaintenanceReaction(t, env, users[0], true, 1)

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceExpired(t, env)
}

func TestLikeStateMaintenanceBlocksPostCountMismatchIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 2)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 1, 2, []uint{users[0]}, now)

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceNotExpired(t, env)
}

func TestLikeStateMaintenanceBlocksPostVersionMismatchIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 10)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 2, 9, []uint{users[0], users[1]}, now)

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceNotExpired(t, env)
}

func TestLikeStateMaintenanceBlocksRedisCardinalityMismatchIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, users := newLikeStateMaintenanceFixture(t, 2, 2)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 1, 2, []uint{users[0]}, now)
	if err := env.redis.Set(likes.CountKey(env.post.ID), "2", 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceNotExpired(t, env)
}

func TestLikeStateMaintenanceBlocksSnapshotQueueBusyIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 2, 10)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 2, 10, []uint{env.users[0], env.users[1]}, now)
	postIDString := strconv.FormatUint(uint64(env.post.ID), 10)

	queueStates := []struct {
		name  string
		block func() error
		clear func() error
	}{
		{
			name:  "dirty",
			block: func() error { return env.redis.SAdd(likes.DirtyKey, postIDString).Err() },
			clear: func() error { return env.redis.SRem(likes.DirtyKey, postIDString).Err() },
		},
		{
			name: "processing",
			block: func() error {
				return env.redis.ZAdd(likes.ProcessingKey, &redis.Z{Score: float64(now.UnixMilli()), Member: postIDString}).Err()
			},
			clear: func() error { return env.redis.ZRem(likes.ProcessingKey, postIDString).Err() },
		},
		{
			name:  "claim",
			block: func() error { return env.redis.HSet(likes.ClaimsKey, postIDString, "maintenance-claim").Err() },
			clear: func() error { return env.redis.HDel(likes.ClaimsKey, postIDString).Err() },
		},
	}
	for _, queueState := range queueStates {
		t.Run(queueState.name, func(t *testing.T) {
			if err := env.store.TouchExpiryCandidate(t.Context(), env.post.ID, now.Add(-2*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := queueState.block(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := queueState.clear(); err != nil {
					t.Errorf("clear queue marker: %v", err)
				}
			}()
			if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
				t.Fatal(err)
			}
			assertLikeStateMaintenanceNotExpired(t, env)
		})
	}
}

func TestLikeStateMaintenanceExpiresZeroLikeStateIntegration(t *testing.T) {
	configureLikeStateMaintenanceExpiry(t)
	env, _ := newLikeStateMaintenanceFixture(t, 0, 0)
	now := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	prepareLikeStateMaintenanceCandidate(t, env, 0, 0, nil, now)

	if err := verifyIdleLikeStates(t.Context(), env.store, env.db, now); err != nil {
		t.Fatal(err)
	}
	assertLikeStateMaintenanceExpired(t, env)
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
	if initialized, err := env.store.Initialize(t.Context(), deletedPost.ID, 0, 0, nil); err != nil || !initialized {
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
