package tasks

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

// Manual acceptance against disposable Redis, Kafka and PostgreSQL services.
// It is intentionally opt-in because the default integration CI has no Kafka
// service in its PostgreSQL/Redis job.
func TestLikeRedisKafkaPostgresE2EIntegration(t *testing.T) {
	if os.Getenv("KAFKA_LIKE_E2E_CONFIRM") != "dedicated-disposable" {
		t.Skip("set KAFKA_LIKE_E2E_CONFIRM=dedicated-disposable for real Kafka E2E")
	}
	t.Setenv("USER_LIKE_TTL_ARMING_ENABLED", "false")
	t.Setenv("USER_LIKE_TTL_RESTORE_ENABLED", "true")
	t.Setenv("USER_LIKE_SET_TTL", "72h")
	t.Setenv("USER_LIKE_RESTORE_LOCK_TTL", "5s")
	t.Setenv("USER_LIKE_RESTORE_BATCH_SIZE", "500")
	t.Setenv("USER_LIKE_RESTORE_MAX_RELATIONS", "10000")
	t.Setenv("USER_LIKE_RESTORE_REQUEST_TIMEOUT", "2s")
	t.Setenv("USER_LIKE_RESTORE_CONCURRENCY", "8")
	dsn, redisAddr, broker := os.Getenv("POSTGRES_TEST_DSN"), os.Getenv("REDIS_TEST_ADDR"), os.Getenv("KAFKA_BROKERS")
	if dsn == "" || redisAddr == "" || broker == "" {
		t.Skip("set POSTGRES_TEST_DSN, REDIS_TEST_ADDR and KAFKA_BROKERS")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostReaction{}, &models.PostBehavior{}, &models.UserRecoProfileDirty{}, &models.ConsumerInbox{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	redisDB, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: redisAddr, DB: redisDB})
	t.Cleanup(func() { client.Close() })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{likes.DirtyKey, likes.BehaviorDirtyKey} {
		if count, err := client.SCard(key).Result(); err != nil || count != 0 {
			t.Fatalf("E2E requires an empty disposable Redis queue %s: count=%d err=%v", key, count, err)
		}
	}

	groupSuffix := uuid.NewString()
	snapshotTopic := "goexchange.spec03.snapshot." + groupSuffix
	behaviorTopic := "goexchange.spec03.behavior." + groupSuffix
	conn, err := kafka.DialContext(t.Context(), "tcp", broker)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.CreateTopics(
		kafka.TopicConfig{Topic: snapshotTopic, NumPartitions: 1, ReplicationFactor: 1},
		kafka.TopicConfig{Topic: behaviorTopic, NumPartitions: 1, ReplicationFactor: 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	kafkaConfig := config.KafkaConfig{
		Brokers:           []string{broker},
		LikeSnapshotTopic: snapshotTopic, UserBehaviorTopic: behaviorTopic,
		ActivityEventsTopic: "goexchange.activity.events.v1", ConsumerDLQTopic: "goexchange.consumer.dlq.v1",
		LikeSnapshotGroupID: "spec03-snapshot-" + groupSuffix, UserBehaviorGroupID: "spec03-behavior-" + groupSuffix,
	}
	oldConfig, oldAPIDB, oldDB, oldRedis := config.AppConfig, global.APIDb, global.WorkerDb, global.RedisDB
	config.AppConfig = &config.Config{Kafka: kafkaConfig}
	global.APIDb, global.WorkerDb, global.RedisDB = db, db, client
	t.Cleanup(func() {
		config.AppConfig, global.APIDb, global.WorkerDb, global.RedisDB = oldConfig, oldAPIDB, oldDB, oldRedis
	})

	actor := models.User{Username: "spec03-actor-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "spec03-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&actor, &author}).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: author.ID, Content: "SPEC-03 real Kafka E2E", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{post.ID}
	t.Cleanup(func() {
		_ = cleanupLikeRelayIntegrationState(client, postIDs, []uint{actor.ID})
		client.Del(likes.UserLikesKey(actor.ID), likes.UserLikesOrderKey(actor.ID), likes.UserLikesRestoreLockKey(actor.ID))
		client.HDel(likes.UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(actor.ID), 10))
		db.Unscoped().Where("post_id IN ? AND user_id = ?", postIDs, actor.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ? AND user_id = ?", postIDs, actor.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("consumer_name IN ?", []string{kafkaConfig.LikeSnapshotGroupID, kafkaConfig.UserBehaviorGroupID}).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("user_id IN ?", []uint{actor.ID, author.ID}).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{actor.ID, author.ID}).Delete(&models.User{})
	})

	store := likes.NewStore(client)
	if created, err := initializeLikeStore(store, t.Context(), post.ID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(t.Context(), actor.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	var consumers sync.WaitGroup
	consumers.Add(2)
	go func() { defer consumers.Done(); runLikeSnapshotProjectionConsumer(ctx) }()
	go func() { defer consumers.Done(); runUserBehaviorProjectionConsumer(ctx) }()
	publisher, err := eventing.NewKafkaPublisher(kafkaConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	deadline := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	latencies := make([]time.Duration, 0, 20)
	type likeStep struct {
		method  string
		handler gin.HandlerFunc
	}
	for round := 0; round < 20; round++ {
		if round > 0 {
			post = models.Post{AuthorID: author.ID, Content: "SPEC-03 real Kafka E2E", Visibility: "public"}
			if err := db.Create(&post).Error; err != nil {
				t.Fatal(err)
			}
			postIDs = append(postIDs, post.ID)
			if created, err := initializeLikeStore(store, ctx, post.ID, 0, 0, nil); err != nil || !created {
				t.Fatalf("initialize Post created=%t err=%v", created, err)
			}
		}
		started := time.Now()
		steps := []likeStep{{http.MethodPut, controllers.LikePost}}
		if round == 0 {
			steps = append(steps, likeStep{http.MethodDelete, controllers.UnlikePost}, likeStep{http.MethodPut, controllers.LikePost})
		}
		for _, step := range steps {
			response := invokeLikeStateClosureHandler(t, step.method, "/api/posts/"+strconv.FormatUint(uint64(post.ID), 10)+"/like", post.ID, actor.ID, step.handler)
			if response.Code != http.StatusOK {
				t.Fatalf("Like API status=%d body=%s", response.Code, response.Body.String())
			}
		}
		wantVersion := int64(1)
		if round == 0 {
			wantVersion = 3
		}
		if err := runLikeSnapshotRelayBatch(ctx, store, publisher); err != nil {
			t.Fatal(err)
		}
		if err := runLikeBehaviorRelayBatch(ctx, store, publisher); err != nil {
			t.Fatal(err)
		}
		for {
			var projected models.Post
			var reaction models.PostReaction
			postErr := db.Where("id = ?", post.ID).First(&projected).Error
			reactionResult := db.Where("user_id = ? AND post_id = ?", actor.ID, post.ID).Find(&reaction)
			if postErr == nil && reactionResult.Error == nil && reactionResult.RowsAffected == 1 && projected.LikeCount == 1 && projected.LikeSyncVersion == wantVersion && reaction.Liked && reaction.Version == wantVersion {
				latencies = append(latencies, time.Since(started))
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("E2E round=%d did not converge: Post count=%d version=%d reaction=%+v post_err=%v reaction_err=%v: %v", round, projected.LikeCount, projected.LikeSyncVersion, reaction, postErr, reactionResult.Error, ctx.Err())
			case <-deadline.C:
			}
		}
		if state, err := store.Get(t.Context(), actor.ID, post.ID); err != nil || state.Count != 1 || state.Version != wantVersion || !state.Liked {
			t.Fatalf("Redis round=%d final state=%+v err=%v", round, state, err)
		}
		if member, err := client.SIsMember(likes.UserLikesKey(actor.ID), post.ID).Result(); err != nil || !member {
			t.Fatalf("Redis round=%d relation member=%t err=%v", round, member, err)
		}
	}

	firstPostID := postIDs[0]
	secondPost := models.Post{AuthorID: author.ID, Content: "SPEC-04 cold restore E2E", Visibility: "private"}
	if err := db.Create(&secondPost).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, secondPost.ID)
	if created, err := initializeLikeStore(store, ctx, secondPost.ID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize cold restore Post created=%t err=%v", created, err)
	}
	response := invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(secondPost.ID), 10)+"/like", secondPost.ID, actor.ID, controllers.LikePost)
	if response.Code != http.StatusOK {
		t.Fatalf("Like before cold restore status=%d body=%s", response.Code, response.Body.String())
	}
	if err := runLikeSnapshotRelayBatch(ctx, store, publisher); err != nil {
		t.Fatal(err)
	}
	if err := runLikeBehaviorRelayBatch(ctx, store, publisher); err != nil {
		t.Fatal(err)
	}
	if err := waitLikeProjectionState(ctx, db, actor.ID, secondPost.ID, true, 1, 1, 1); err != nil {
		t.Fatalf("second Post Like did not project before cold restore: %v", err)
	}
	if _, err := client.Eval(`
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local expires_at = now_ms + tonumber(ARGV[2])
redis.call('PEXPIREAT', KEYS[1], expires_at)
redis.call('PEXPIREAT', KEYS[3], expires_at)
redis.call('HSET', KEYS[2], ARGV[1], tostring(expires_at))
return expires_at
`, []string{likes.UserLikesKey(actor.ID), likes.UserLikesExpiryLedgerKey, likes.UserLikesOrderKey(actor.ID)}, strconv.FormatUint(uint64(actor.ID), 10), 300).Result(); err != nil {
		t.Fatalf("arm short test-only User Set expiry: %v", err)
	}
	expiryDeadline := time.Now().Add(5 * time.Second)
	for {
		exists, err := client.Exists(likes.UserLikesKey(actor.ID), likes.UserLikesOrderKey(actor.ID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(expiryDeadline) {
			t.Fatal("User Like Set did not reach its test-only TTL deadline")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	response = invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(firstPostID), 10)+"/like", firstPostID, actor.ID, controllers.LikePost)
	if response.Code != http.StatusOK {
		t.Fatalf("idempotent Like after cold restore status=%d body=%s", response.Code, response.Body.String())
	}
	response = invokeLikeStateClosureHandler(t, http.MethodDelete, "/api/posts/"+strconv.FormatUint(uint64(secondPost.ID), 10)+"/like", secondPost.ID, actor.ID, controllers.UnlikePost)
	if response.Code != http.StatusOK {
		t.Fatalf("Unlike after cold restore status=%d body=%s", response.Code, response.Body.String())
	}
	if err := runLikeSnapshotRelayBatch(ctx, store, publisher); err != nil {
		t.Fatal(err)
	}
	if err := runLikeBehaviorRelayBatch(ctx, store, publisher); err != nil {
		t.Fatal(err)
	}
	if err := waitLikeProjectionState(ctx, db, actor.ID, secondPost.ID, false, 2, 0, 2); err != nil {
		t.Fatalf("Unlike after cold restore did not project: %v", err)
	}
	for _, postID := range []uint{firstPostID, secondPost.ID} {
		state, err := store.Get(t.Context(), actor.ID, postID)
		if err != nil {
			t.Fatalf("read Redis Like after cold restore Post=%d: %v", postID, err)
		}
		if postID == firstPostID && (state.Count != 1 || state.Version != 3 || !state.Liked) {
			t.Fatalf("idempotent restored Like changed first Post state: %+v", state)
		}
		if postID == secondPost.ID && (state.Count != 0 || state.Version != 2 || state.Liked) {
			t.Fatalf("Unlike restored relation has wrong Redis state: %+v", state)
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("HTTP Like -> Redis -> real Kafka -> PostgreSQL rounds=%d p50=%s p95=%s p99=%s", len(latencies), latencies[9], latencies[18], latencies[19])
	t.Logf("sorted end-to-end samples=%v", latencies)
	cancel()
	consumers.Wait()
}

func waitLikeProjectionState(ctx context.Context, db *gorm.DB, userID, postID uint, liked bool, reactionVersion, postCount, postVersion int64) error {
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var post models.Post
	var reaction models.PostReaction
	var postErr, reactionErr error
	for {
		postErr = db.WithContext(waitCtx).Where("id = ?", postID).First(&post).Error
		reactionErr = db.WithContext(waitCtx).Where("user_id = ? AND post_id = ?", userID, postID).First(&reaction).Error
		if postErr == nil && reactionErr == nil && reaction.Liked == liked && reaction.Version == reactionVersion &&
			post.LikeCount == postCount && post.LikeSyncVersion == postVersion {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("projection did not converge: post=%+v reaction=%+v post_err=%v reaction_err=%v: %w", post, reaction, postErr, reactionErr, waitCtx.Err())
		case <-ticker.C:
		}
	}
}
