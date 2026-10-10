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

type recordingKafkaPublisher struct {
	inner  *eventing.KafkaPublisher
	events []eventing.Envelope
}

func (p *recordingKafkaPublisher) Publish(ctx context.Context, event eventing.Envelope) error {
	if err := p.inner.Publish(ctx, event); err != nil {
		return err
	}
	p.events = append(p.events, event)
	return nil
}

func (p *recordingKafkaPublisher) PublishBatch(ctx context.Context, events []eventing.Envelope) error {
	if err := p.inner.PublishBatch(ctx, events); err != nil {
		return err
	}
	p.events = append(p.events, events...)
	return nil
}

func (p *recordingKafkaPublisher) Close() error { return p.inner.Close() }

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

	actor := models.User{Username: "spec04-actor-" + uuid.NewString(), Password: "test"}
	author := models.User{Username: "spec03-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&actor, &author}).Error; err != nil {
		t.Fatal(err)
	}
	post := models.Post{AuthorID: author.ID, Content: "SPEC-03 real Kafka E2E", Visibility: "public"}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{post.ID}
	userIDs := []uint{actor.ID}
	t.Cleanup(func() {
		_ = cleanupLikeRelayIntegrationState(client, postIDs, userIDs)
		client.Del(likes.UserLikesKey(actor.ID), likes.UserLikesOrderKey(actor.ID), likes.UserLikesRestoreLockKey(actor.ID))
		client.HDel(likes.UserLikesExpiryLedgerKey, strconv.FormatUint(uint64(actor.ID), 10))
		db.Unscoped().Where("post_id IN ? AND user_id IN ?", postIDs, userIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ? AND user_id IN ?", postIDs, userIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("consumer_name IN ?", []string{kafkaConfig.LikeSnapshotGroupID, kafkaConfig.UserBehaviorGroupID}).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("user_id IN ?", append(append([]uint(nil), userIDs...), author.ID)).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", append(append([]uint(nil), userIDs...), author.ID)).Delete(&models.User{})
	})

	store := likes.NewStore(client)
	if created, err := initializeLikeStore(store, t.Context(), post.ID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize Post created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(t.Context(), actor.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	var consumers sync.WaitGroup
	consumers.Add(2)
	go func() { defer consumers.Done(); runLikeSnapshotProjectionConsumer(ctx) }()
	go func() { defer consumers.Done(); runUserBehaviorProjectionConsumer(ctx) }()
	defer func() {
		cancel()
		consumers.Wait()
	}()
	innerPublisher, err := eventing.NewKafkaPublisher(kafkaConfig)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingKafkaPublisher{inner: innerPublisher}
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

	// Real Kafka acceptance for the cap path: three projected Likes followed
	// by a fourth Like that automatically unlikes the oldest relation.
	capActor := models.User{Username: "spec04-cap-actor-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&capActor).Error; err != nil {
		t.Fatal(err)
	}
	userIDs = append(userIDs, capActor.ID)
	lifecycleSettings, err := config.UserLikeLifecycleSettings()
	if err != nil {
		t.Fatal(err)
	}
	capStore := likes.NewStoreWithUserLikeSettingsAndRelationLimit(client, lifecycleSettings, 3)
	capPosts := make([]models.Post, 4)
	capPostIDs := make([]uint, 4)
	for index := range capPosts {
		capPosts[index] = models.Post{AuthorID: author.ID, Content: fmt.Sprintf("SPEC-04 cap E2E %d", index+1), Visibility: "public"}
		if err := db.Create(&capPosts[index]).Error; err != nil {
			t.Fatal(err)
		}
		capPostIDs[index] = capPosts[index].ID
		postIDs = append(postIDs, capPosts[index].ID)
		if created, err := initializeLikeStore(capStore, ctx, capPosts[index].ID, 0, 0, nil); err != nil || !created {
			t.Fatalf("initialize cap Post %d created=%t err=%v", capPosts[index].ID, created, err)
		}
	}
	if err := capStore.InitializeUserEmpty(ctx, capActor.ID); err != nil {
		t.Fatal(err)
	}
	for _, postID := range capPostIDs[:3] {
		result, err := capStore.Mutate(ctx, capActor.ID, postID, true)
		if err != nil || !result.Changed || result.EvictedPostID != 0 {
			t.Fatalf("initial cap Like Post=%d result=%+v err=%v", postID, result, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := runLikeSnapshotRelayBatch(ctx, capStore, publisher); err != nil {
		t.Fatal(err)
	}
	if err := runLikeBehaviorRelayBatch(ctx, capStore, publisher); err != nil {
		t.Fatal(err)
	}
	var firstCapLike eventing.Envelope
	var firstCapSnapshot eventing.Envelope
	for _, postID := range capPostIDs[:3] {
		if err := waitLikeProjectionState(ctx, db, capActor.ID, postID, true, 1, 1, 1); err != nil {
			t.Fatalf("initial cap Like Post=%d did not project: %v", postID, err)
		}
		behaviorEventID := fmt.Sprintf("like-state:%d:%d:1", capActor.ID, postID)
		behaviorEvent, ok := findLikeStateClosureEvent(publisher.events, behaviorEventID)
		if !ok || behaviorEvent.Type != eventing.EventTypePostLiked {
			t.Fatalf("real Kafka Like event missing or invalid for Post=%d: %+v", postID, behaviorEvent)
		}
		score, err := client.ZScore(likes.UserLikesOrderKey(capActor.ID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || score != float64(behaviorEvent.OccurredAt.UnixMicro()) {
			t.Fatalf("Post=%d Redis order score=%v event time=%s err=%v", postID, score, behaviorEvent.OccurredAt, err)
		}
		var reaction models.PostReaction
		if err := db.Where("user_id = ? AND post_id = ?", capActor.ID, postID).First(&reaction).Error; err != nil {
			t.Fatal(err)
		}
		if !reaction.StateChangedAt.Equal(behaviorEvent.OccurredAt) {
			t.Fatalf("Post=%d PostgreSQL state_changed_at=%s Kafka occurred_at=%s", postID, reaction.StateChangedAt, behaviorEvent.OccurredAt)
		}
		if postID == capPostIDs[0] {
			firstCapLike = behaviorEvent
			firstCapSnapshot, ok = findLikeStateClosureEvent(publisher.events, fmt.Sprintf("like-snapshot:%d:1", postID))
			if !ok {
				t.Fatalf("initial snapshot event missing for Post=%d", postID)
			}
		}
	}

	eviction, err := capStore.Mutate(ctx, capActor.ID, capPostIDs[3], true)
	if err != nil || !eviction.Changed || eviction.EvictedPostID != capPostIDs[0] || eviction.ActiveRelations != 3 {
		t.Fatalf("cap eviction result=%+v err=%v, want oldest Post %d and three active relations", eviction, err, capPostIDs[0])
	}
	if err := runLikeSnapshotRelayBatch(ctx, capStore, publisher); err != nil {
		t.Fatal(err)
	}
	if err := runLikeBehaviorRelayBatch(ctx, capStore, publisher); err != nil {
		t.Fatal(err)
	}
	for index, postID := range capPostIDs {
		liked, reactionVersion, postCount, postVersion := true, int64(1), int64(1), int64(1)
		if index == 0 {
			liked, reactionVersion, postCount, postVersion = false, 2, 0, 2
		}
		if err := waitLikeProjectionState(ctx, db, capActor.ID, postID, liked, reactionVersion, postCount, postVersion); err != nil {
			t.Fatalf("post-eviction projection Post=%d did not converge: %v", postID, err)
		}
	}
	unlikeEvent, ok := findLikeStateClosureEvent(publisher.events, fmt.Sprintf("like-state:%d:%d:2", capActor.ID, capPostIDs[0]))
	if !ok || unlikeEvent.Type != eventing.EventTypePostUnliked {
		t.Fatalf("automatic eviction unlike event missing: %+v", unlikeEvent)
	}
	capP4Like, ok := findLikeStateClosureEvent(publisher.events, fmt.Sprintf("like-state:%d:%d:1", capActor.ID, capPostIDs[3]))
	if !ok || capP4Like.Type != eventing.EventTypePostLiked {
		t.Fatalf("fourth Post Like event missing: %+v", capP4Like)
	}
	wantActive := []string{
		strconv.FormatUint(uint64(capPostIDs[1]), 10),
		strconv.FormatUint(uint64(capPostIDs[2]), 10),
		strconv.FormatUint(uint64(capPostIDs[3]), 10),
	}
	actualSet, err := client.SMembers(likes.UserLikesKey(capActor.ID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	actualActive := make([]string, 0, len(actualSet))
	for _, member := range actualSet {
		if member != likes.UserLikesInitSentinel {
			actualActive = append(actualActive, member)
		}
	}
	sort.Strings(actualActive)
	sort.Strings(wantActive)
	if len(actualActive) != len(wantActive) {
		t.Fatalf("Redis active User Likes=%v want=%v", actualActive, wantActive)
	}
	for index := range wantActive {
		if actualActive[index] != wantActive[index] {
			t.Fatalf("Redis active User Likes=%v want=%v", actualActive, wantActive)
		}
	}
	orderedAfterEviction, err := client.ZRange(likes.UserLikesOrderKey(capActor.ID), 0, -1).Result()
	if err != nil || len(orderedAfterEviction) != 3 {
		t.Fatalf("Redis order after eviction=%v err=%v", orderedAfterEviction, err)
	}
	for index, postID := range capPostIDs[1:] {
		if orderedAfterEviction[index] != strconv.FormatUint(uint64(postID), 10) {
			t.Fatalf("Redis order after eviction=%v want P2,P3,P4=%v", orderedAfterEviction, wantActive)
		}
	}

	eventByPost := map[uint]eventing.Envelope{
		capPostIDs[0]: unlikeEvent,
		capPostIDs[1]: mustRecordedCapLikeEvent(t, publisher.events, capActor.ID, capPostIDs[1], 1),
		capPostIDs[2]: mustRecordedCapLikeEvent(t, publisher.events, capActor.ID, capPostIDs[2], 1),
		capPostIDs[3]: capP4Like,
	}
	for index, postID := range capPostIDs {
		state, err := capStore.LoadFullState(ctx, postID)
		if err != nil {
			t.Fatal(err)
		}
		wantCount, wantVersion := int64(1), int64(1)
		if index == 0 {
			wantCount, wantVersion = 0, 2
		}
		if state.Count != wantCount || state.Version != wantVersion {
			t.Fatalf("Redis Post=%d aggregate=%+v want count=%d version=%d", postID, state, wantCount, wantVersion)
		}
		var post models.Post
		var reaction models.PostReaction
		if err := db.First(&post, postID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Where("user_id = ? AND post_id = ?", capActor.ID, postID).First(&reaction).Error; err != nil {
			t.Fatal(err)
		}
		if post.LikeCount != state.Count || post.LikeSyncVersion != state.Version || reaction.Liked != (index != 0) || !reaction.StateChangedAt.Equal(eventByPost[postID].OccurredAt) {
			t.Fatalf("Post=%d PostgreSQL snapshot=%+v reaction=%+v Redis=%+v event_time=%s", postID, post, reaction, state, eventByPost[postID].OccurredAt)
		}
	}

	// A fresh-ID copy of the old Like reaches the real consumer after the
	// eviction Unlike. The PostgreSQL version gate must keep the newer false state.
	staleLike := firstCapLike
	staleLike.ID = uuid.NewString()
	if err := publisher.Publish(ctx, staleLike); err != nil {
		t.Fatalf("publish stale Like event: %v", err)
	}
	if err := waitLikeConsumerInboxEvent(ctx, db, kafkaConfig.UserBehaviorGroupID, staleLike.ID); err != nil {
		t.Fatalf("stale Like event was not consumed: %v", err)
	}
	if err := publisher.Publish(ctx, unlikeEvent); err != nil {
		t.Fatalf("republish duplicate eviction Unlike: %v", err)
	}
	staleSnapshot := firstCapSnapshot
	staleSnapshot.ID = uuid.NewString()
	if err := publisher.Publish(ctx, staleSnapshot); err != nil {
		t.Fatalf("publish stale snapshot event: %v", err)
	}
	if err := waitLikeConsumerInboxEvent(ctx, db, kafkaConfig.LikeSnapshotGroupID, staleSnapshot.ID); err != nil {
		t.Fatalf("stale snapshot event was not consumed: %v", err)
	}
	if err := publisher.Publish(ctx, staleSnapshot); err != nil {
		t.Fatalf("republish duplicate stale snapshot: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	var finalP1 models.Post
	var finalP1Reaction models.PostReaction
	if err := db.First(&finalP1, capPostIDs[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("user_id = ? AND post_id = ?", capActor.ID, capPostIDs[0]).First(&finalP1Reaction).Error; err != nil {
		t.Fatal(err)
	}
	if finalP1.LikeCount != 0 || finalP1.LikeSyncVersion != 2 || finalP1Reaction.Liked || finalP1Reaction.Version != 2 {
		t.Fatalf("stale or duplicate Kafka event changed final P1 state: post=%+v reaction=%+v", finalP1, finalP1Reaction)
	}

	// Expire the complete User relation atomically, recover it from PostgreSQL,
	// and confirm the original Redis order is restored without new timestamps.
	if _, err := client.Eval(`
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local expires_at = now_ms + tonumber(ARGV[2])
redis.call('PEXPIREAT', KEYS[1], expires_at)
redis.call('PEXPIREAT', KEYS[3], expires_at)
redis.call('HSET', KEYS[2], ARGV[1], tostring(expires_at))
return expires_at
`, []string{likes.UserLikesKey(capActor.ID), likes.UserLikesExpiryLedgerKey, likes.UserLikesOrderKey(capActor.ID)}, strconv.FormatUint(uint64(capActor.ID), 10), 300).Result(); err != nil {
		t.Fatalf("arm cap actor test-only TTL: %v", err)
	}
	capExpiryDeadline := time.Now().Add(5 * time.Second)
	for {
		exists, err := client.Exists(likes.UserLikesKey(capActor.ID), likes.UserLikesOrderKey(capActor.ID)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(capExpiryDeadline) {
			t.Fatal("cap actor User Like keys did not expire")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	response = invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(capPostIDs[1]), 10)+"/like", capPostIDs[1], capActor.ID, controllers.LikePost)
	if response.Code != http.StatusOK {
		t.Fatalf("idempotent Like after cap actor cold restore status=%d body=%s", response.Code, response.Body.String())
	}
	orderedAfterRestore, err := client.ZRange(likes.UserLikesOrderKey(capActor.ID), 0, -1).Result()
	if err != nil || len(orderedAfterRestore) != len(orderedAfterEviction) {
		t.Fatalf("Redis order after cold restore=%v err=%v", orderedAfterRestore, err)
	}
	for index := range orderedAfterEviction {
		if orderedAfterRestore[index] != orderedAfterEviction[index] {
			t.Fatalf("Redis order changed after cold restore: before=%v after=%v", orderedAfterEviction, orderedAfterRestore)
		}
	}
	for _, postID := range capPostIDs[1:] {
		var reaction models.PostReaction
		if err := db.Where("user_id = ? AND post_id = ?", capActor.ID, postID).First(&reaction).Error; err != nil {
			t.Fatal(err)
		}
		score, err := client.ZScore(likes.UserLikesOrderKey(capActor.ID), strconv.FormatUint(uint64(postID), 10)).Result()
		if err != nil || score != float64(reaction.StateChangedAt.UnixMicro()) {
			t.Fatalf("restored Post=%d score=%v PG state_changed_at=%s err=%v", postID, score, reaction.StateChangedAt, err)
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("HTTP Like -> Redis -> real Kafka -> PostgreSQL rounds=%d p50=%s p95=%s p99=%s", len(latencies), latencies[9], latencies[18], latencies[19])
	t.Logf("sorted end-to-end samples=%v", latencies)
}

func mustRecordedCapLikeEvent(t *testing.T, events []eventing.Envelope, userID, postID uint, version int64) eventing.Envelope {
	t.Helper()
	eventID := fmt.Sprintf("like-state:%d:%d:%d", userID, postID, version)
	event, ok := findLikeStateClosureEvent(events, eventID)
	if !ok {
		t.Fatalf("Kafka Like event %q missing", eventID)
	}
	if event.Type != eventing.EventTypePostLiked {
		t.Fatalf("Kafka event %q type=%q want %q", eventID, event.Type, eventing.EventTypePostLiked)
	}
	return event
}

func waitLikeConsumerInboxEvent(ctx context.Context, db *gorm.DB, consumerName, eventID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int64
		err := db.WithContext(waitCtx).Model(&models.ConsumerInbox{}).
			Where("consumer_name = ? AND event_id = ?", consumerName, eventID).
			Count(&count).Error
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("consumer %q did not record event %q: %w", consumerName, eventID, waitCtx.Err())
		case <-ticker.C:
		}
	}
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
