package controllers

import (
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestDeletePostPurgesOnlyTargetRedisLikeStateIntegration(t *testing.T) {
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}
	db := openPostDeleteIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostReaction{}); err != nil {
		t.Fatal(err)
	}
	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	redisClient := openPostLikeIntegrationRedis(t)

	owner := models.User{Username: "purge-owner-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "purge-other-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&owner, &other}).Error; err != nil {
		t.Fatal(err)
	}
	target := models.Post{AuthorID: owner.ID, Content: "target post", Visibility: "public"}
	unrelated := models.Post{AuthorID: owner.ID, Content: "unrelated post", Visibility: "public"}
	if err := db.Create(&[]*models.Post{&target, &unrelated}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{target.ID, unrelated.ID}
	pairTarget := likes.BehaviorPair(other.ID, target.ID)
	pairUnrelated := likes.BehaviorPair(other.ID, unrelated.ID)
	cleanup := func() {
		for _, postID := range postIDs {
			redisClient.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
			redisClient.SRem(likes.DirtyKey, postID)
			redisClient.ZRem(likes.ProcessingKey, postID)
			postIDString := strconv.FormatUint(uint64(postID), 10)
			redisClient.HDel(likes.ClaimsKey, postIDString)
			redisClient.SRem(likes.RegistryKey, postID)
			redisClient.ZRem(likes.ExpiryCandidatesKey, postIDString)
			redisClient.HDel(likes.RecoverableVersionsKey, postIDString)
		}
		for _, pair := range []string{pairTarget, pairUnrelated} {
			redisClient.SRem(likes.BehaviorDirtyKey, pair)
			redisClient.HDel(likes.BehaviorStateKey, pair)
			redisClient.ZRem(likes.BehaviorProcessingKey, pair)
			redisClient.HDel(likes.BehaviorClaimsKey, pair)
		}
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, other.ID}).Delete(&models.User{})
	}
	t.Cleanup(cleanup)

	store := likes.NewStore(redisClient)
	for _, postID := range postIDs {
		if created, err := store.Initialize(t.Context(), postID, 1, 4, []uint{other.ID}); err != nil || !created {
			t.Fatalf("initialize post=%d created=%t err=%v", postID, created, err)
		}
	}
	redisClient.SAdd(likes.DirtyKey, target.ID, unrelated.ID)
	redisClient.ZAdd(likes.ProcessingKey, &redis.Z{Score: 1, Member: target.ID}, &redis.Z{Score: 2, Member: unrelated.ID})
	redisClient.HSet(likes.ClaimsKey, strconv.FormatUint(uint64(target.ID), 10), "target-claim", strconv.FormatUint(uint64(unrelated.ID), 10), "unrelated-claim")
	redisClient.SAdd(likes.BehaviorDirtyKey, pairTarget, pairUnrelated)
	redisClient.HSet(likes.BehaviorStateKey, pairTarget, "target-behavior", pairUnrelated, "unrelated-behavior")
	redisClient.ZAdd(likes.BehaviorProcessingKey, &redis.Z{Score: 1, Member: pairTarget}, &redis.Z{Score: 2, Member: pairUnrelated})
	redisClient.HSet(likes.BehaviorClaimsKey, pairTarget, "target-behavior-claim", pairUnrelated, "unrelated-behavior-claim")
	if err := db.Create(&models.PostReaction{
		UserID: other.ID, PostID: target.ID, Reaction: models.PostReactionLike, Liked: true,
		Version: 1, UpdatedAt: time.Now().UTC(), StateChangedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, recorder := newPostDeleteContext(strconv.FormatUint(uint64(target.ID), 10), &owner.ID)
	DeletePost(ctx)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertTargetLikeKeysPurged(t, redisClient, target.ID)
	assertUnrelatedLikeKeysRemain(t, redisClient, unrelated.ID)
	for _, check := range []struct {
		key   string
		field string
	}{
		{likes.BehaviorStateKey, pairTarget}, {likes.BehaviorStateKey, pairUnrelated},
		{likes.BehaviorClaimsKey, pairTarget}, {likes.BehaviorClaimsKey, pairUnrelated},
	} {
		if exists, err := redisClient.HExists(check.key, check.field).Result(); err != nil || !exists {
			t.Fatalf("behavior key=%q field=%q exists=%t err=%v", check.key, check.field, exists, err)
		}
	}
	var reactionCount int64
	if err := db.Model(&models.PostReaction{}).Where("user_id = ? AND post_id = ?", other.ID, target.ID).Count(&reactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if reactionCount != 1 {
		t.Fatalf("relational target reactions=%d want 1", reactionCount)
	}
}

func assertTargetLikeKeysPurged(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	for _, key := range []string{likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("target key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("target dirty=%t err=%v", dirty, err)
	}
	if _, err := client.ZScore(likes.ProcessingKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != redis.Nil {
		t.Fatalf("target processing err=%v", err)
	}
	if exists, err := client.HExists(likes.ClaimsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || exists {
		t.Fatalf("target claim exists=%t err=%v", exists, err)
	}
	postIDString := strconv.FormatUint(uint64(postID), 10)
	if registered, err := client.SIsMember(likes.RegistryKey, postID).Result(); err != nil || registered {
		t.Fatalf("target registry=%t err=%v", registered, err)
	}
	if _, err := client.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("target expiry candidate err=%v", err)
	}
	if marker, err := client.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker {
		t.Fatalf("target recoverable marker=%t err=%v", marker, err)
	}
}

func assertUnrelatedLikeKeysRemain(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	for _, key := range []string{likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 1 {
			t.Fatalf("unrelated key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || !dirty {
		t.Fatalf("unrelated dirty=%t err=%v", dirty, err)
	}
	if _, err := client.ZScore(likes.ProcessingKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil {
		t.Fatalf("unrelated processing err=%v", err)
	}
	if exists, err := client.HExists(likes.ClaimsKey, strconv.FormatUint(uint64(postID), 10)).Result(); err != nil || !exists {
		t.Fatalf("unrelated claim exists=%t err=%v", exists, err)
	}
	postIDString := strconv.FormatUint(uint64(postID), 10)
	if registered, err := client.SIsMember(likes.RegistryKey, postID).Result(); err != nil || !registered {
		t.Fatalf("unrelated registry=%t err=%v", registered, err)
	}
	if _, err := client.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != nil {
		t.Fatalf("unrelated expiry candidate err=%v", err)
	}
	if marker, err := client.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker {
		t.Fatalf("unrelated recoverable marker=%t err=%v", marker, err)
	}
}
