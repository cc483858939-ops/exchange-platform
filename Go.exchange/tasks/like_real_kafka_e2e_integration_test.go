package tasks

import (
	"context"
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
)

// Manual acceptance against disposable Redis, Kafka and PostgreSQL services.
// It is intentionally opt-in because the default integration CI has no Kafka
// service in its PostgreSQL/Redis job.
func TestLikeRedisKafkaPostgresE2EIntegration(t *testing.T) {
	if os.Getenv("KAFKA_LIKE_E2E_CONFIRM") != "dedicated-disposable" {
		t.Skip("set KAFKA_LIKE_E2E_CONFIRM=dedicated-disposable for real Kafka E2E")
	}
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
		client.Del(likes.UserLikesKey(actor.ID))
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
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("HTTP Like -> Redis -> real Kafka -> PostgreSQL rounds=%d p50=%s p95=%s p99=%s", len(latencies), latencies[9], latencies[18], latencies[19])
	t.Logf("sorted end-to-end samples=%v", latencies)
	cancel()
	consumers.Wait()
}
