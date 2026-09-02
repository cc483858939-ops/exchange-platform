package tasks

import (
	"context"
	"os"
	"strconv"
	"testing"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCanonicalPostLikeRedisToPostgresProjectionIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Post{},
		&models.PostReaction{},
		&models.PostBehavior{},
		&models.UserRecoProfileDirty{},
		&models.ConsumerInbox{},
		&models.OutboxEvent{},
	); err != nil {
		t.Fatal(err)
	}

	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr, DB: redisDB})
	if err := redisClient.Ping().Err(); err != nil {
		redisClient.Close()
		t.Fatal(err)
	}
	if err := resetLikeRelayIntegrationQueues(redisClient); err != nil {
		redisClient.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := redisClient.Close(); err != nil {
			t.Errorf("close Redis integration client: %v", err)
		}
	})

	originalDB, originalRedis, originalConfig := global.Db, global.RedisDB, config.AppConfig
	snapshotGroup := "like-snapshot-projection-" + uuid.NewString()
	behaviorGroup := "like-behavior-projection-" + uuid.NewString()
	var actor models.User
	var author models.User
	var post models.Post
	var snapshotEventID string
	var behaviorEventID string
	var reactionAggregateID string
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{
		LikeSnapshotGroupID: snapshotGroup,
		UserBehaviorGroupID: behaviorGroup,
		ActivityEventsTopic: "goexchange.activity.events.v1",
	}}
	global.Db = db
	global.RedisDB = redisClient

	t.Cleanup(func() {
		if err := resetLikeRelayIntegrationQueues(redisClient); err != nil {
			t.Errorf("reset Like relay integration queues: %v", err)
		}
	})
	t.Cleanup(func() {
		if post.ID != 0 {
			if err := cleanupLikeRelayIntegrationState(redisClient, []uint{post.ID}, []uint{actor.ID}); err != nil {
				t.Errorf("cleanup Like projection Redis state: %v", err)
			}
			if actor.ID != 0 {
				db.Unscoped().Where("post_id = ? AND user_id = ?", post.ID, actor.ID).Delete(&models.PostReaction{})
				db.Unscoped().Where("post_id = ? AND user_id = ?", post.ID, actor.ID).Delete(&models.PostBehavior{})
			}
			db.Unscoped().Where("id = ?", post.ID).Delete(&models.Post{})
		}
		if reactionAggregateID != "" {
			db.Unscoped().Where("aggregate_id = ?", reactionAggregateID).Delete(&models.OutboxEvent{})
		}
		db.Unscoped().Where("consumer_name IN ?", []string{snapshotGroup, behaviorGroup}).Delete(&models.ConsumerInbox{})
		dirtyUserIDs := make([]uint, 0, 2)
		if actor.ID != 0 {
			dirtyUserIDs = append(dirtyUserIDs, actor.ID)
		}
		if author.ID != 0 {
			dirtyUserIDs = append(dirtyUserIDs, author.ID)
		}
		if len(dirtyUserIDs) > 0 {
			db.Unscoped().Where("user_id IN ?", dirtyUserIDs).Delete(&models.UserRecoProfileDirty{})
			db.Unscoped().Where("id IN ?", dirtyUserIDs).Delete(&models.User{})
		}
		global.Db, global.RedisDB, config.AppConfig = originalDB, originalRedis, originalConfig
	})

	actor = models.User{Username: "like-projection-actor-" + uuid.NewString(), Password: "test"}
	author = models.User{Username: "like-projection-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&actor, &author}).Error; err != nil {
		t.Fatal(err)
	}
	post = models.Post{AuthorID: author.ID, Content: "canonical like projection", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	store := likes.NewStore(redisClient)
	snapshotEventID = "like-snapshot:" + strconv.FormatUint(uint64(post.ID), 10) + ":1"
	behaviorEventID = "like-state:" + strconv.FormatUint(uint64(actor.ID), 10) + ":" + strconv.FormatUint(uint64(post.ID), 10) + ":1"
	reactionAggregateID = strconv.FormatUint(uint64(actor.ID), 10) + ":" + strconv.FormatUint(uint64(post.ID), 10)

	initialized, err := store.Initialize(ctx, post.ID, 0, 0, nil)
	if err != nil || !initialized {
		t.Fatalf("like state initialized=%t err=%v", initialized, err)
	}
	mutation, err := store.Mutate(ctx, actor.ID, post.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Count != 1 || !mutation.Liked || !mutation.Changed || mutation.Version != 1 {
		t.Fatalf("like mutation=%#v", mutation)
	}
	if ready, err := redisClient.Get(likes.ReadyKey(post.ID)).Result(); err != nil || ready != "1" {
		t.Fatalf("redis ready=%q err=%v", ready, err)
	}
	if count, err := redisClient.Get(likes.CountKey(post.ID)).Result(); err != nil || count != "1" {
		t.Fatalf("redis count=%q err=%v", count, err)
	}
	if version, err := redisClient.Get(likes.VersionKey(post.ID)).Result(); err != nil || version != "1" {
		t.Fatalf("redis version=%q err=%v", version, err)
	}
	if liked, err := redisClient.SIsMember(likes.UsersKey(post.ID), strconv.FormatUint(uint64(actor.ID), 10)).Result(); err != nil || !liked {
		t.Fatalf("redis user membership=%t err=%v", liked, err)
	}
	pair := likes.BehaviorPair(actor.ID, post.ID)
	if dirty, err := redisClient.SIsMember(likes.DirtyKey, post.ID).Result(); err != nil || !dirty {
		t.Fatalf("snapshot dirty=%t err=%v", dirty, err)
	}
	if state, err := redisClient.HGet(likes.BehaviorStateKey, pair).Result(); err != nil || state == "" {
		t.Fatalf("behavior state=%q err=%v", state, err)
	}
	if dirty, err := redisClient.SIsMember(likes.BehaviorDirtyKey, pair).Result(); err != nil || !dirty {
		t.Fatalf("behavior dirty=%t err=%v", dirty, err)
	}

	snapshotPublisher := &relayTestPublisher{}
	if err := runLikeSnapshotRelayBatch(ctx, store, snapshotPublisher); err != nil {
		t.Fatal(err)
	}
	if len(snapshotPublisher.events) != 1 || snapshotPublisher.events[0].ID != snapshotEventID {
		t.Fatalf("snapshot events=%#v", snapshotPublisher.events)
	}
	if err := applyLikeSnapshotEvent(snapshotPublisher.events[0]); err != nil {
		t.Fatal(err)
	}

	behaviorPublisher := &relayTestPublisher{}
	if err := runLikeBehaviorRelayBatch(ctx, store, behaviorPublisher); err != nil {
		t.Fatal(err)
	}
	if len(behaviorPublisher.events) != 1 || behaviorPublisher.events[0].ID != behaviorEventID {
		t.Fatalf("behavior events=%#v", behaviorPublisher.events)
	}
	if err := applyUserBehaviorEvent(behaviorPublisher.events[0]); err != nil {
		t.Fatal(err)
	}

	assertCanonicalLikeProjectionRows(t, db, post.ID, actor.ID, mutation.Version)
	assertCanonicalLikeProjectionInbox(t, db, snapshotGroup, behaviorGroup, snapshotEventID, behaviorEventID)
	var dirtyCount int64
	if err := db.Model(&models.UserRecoProfileDirty{}).Where("user_id = ?", actor.ID).Count(&dirtyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 1 {
		t.Fatalf("user recommendation dirty rows=%d want 1", dirtyCount)
	}

	if err := applyLikeSnapshotEvent(snapshotPublisher.events[0]); err != nil {
		t.Fatal(err)
	}
	if err := applyUserBehaviorEvent(behaviorPublisher.events[0]); err != nil {
		t.Fatal(err)
	}
	assertCanonicalLikeProjectionRows(t, db, post.ID, actor.ID, mutation.Version)
	assertCanonicalLikeProjectionInbox(t, db, snapshotGroup, behaviorGroup, snapshotEventID, behaviorEventID)
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).
		Where("event_type = ? AND aggregate_id = ?", eventing.EventTypePostReactionApplied, reactionAggregateID).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("reaction activity outbox rows=%d want 1 after replay", outboxCount)
	}
}

func assertCanonicalLikeProjectionRows(t *testing.T, db *gorm.DB, postID, userID uint, version int64) {
	t.Helper()
	var post models.Post
	if err := db.First(&post, postID).Error; err != nil {
		t.Fatal(err)
	}
	if post.LikeCount != 1 || post.LikeSyncVersion != version {
		t.Fatalf("post projection=%#v want like_count=1 version=%d", post, version)
	}
	var reaction models.PostReaction
	if err := db.Where("user_id = ? AND post_id = ?", userID, postID).First(&reaction).Error; err != nil {
		t.Fatal(err)
	}
	if !reaction.Liked || reaction.Version != version {
		t.Fatalf("reaction projection=%#v want liked=true version=%d", reaction, version)
	}
}

func assertCanonicalLikeProjectionInbox(t *testing.T, db *gorm.DB, snapshotGroup, behaviorGroup, snapshotEventID, behaviorEventID string) {
	t.Helper()
	var snapshotCount, behaviorCount int64
	if err := db.Model(&models.ConsumerInbox{}).Where("consumer_name = ? AND event_id = ?", snapshotGroup, snapshotEventID).Count(&snapshotCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ConsumerInbox{}).Where("consumer_name = ? AND event_id = ?", behaviorGroup, behaviorEventID).Count(&behaviorCount).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 || behaviorCount != 1 {
		t.Fatalf("inbox snapshot=%d behavior=%d want one each", snapshotCount, behaviorCount)
	}
}
