package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type likeStateClosureIntegration struct {
	db            *gorm.DB
	redis         *redis.Client
	store         *likes.Store
	snapshotGroup string
	behaviorGroup string

	author models.User
	actor  models.User
	actor2 models.User
	post   models.Post
	users  []uint
	posts  []uint
}

func openLikeStateClosureIntegration(t *testing.T) *likeStateClosureIntegration {
	t.Helper()
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
		&models.PostArticle{},
		&models.PostRepost{},
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

	env := &likeStateClosureIntegration{
		db:            db,
		redis:         redisClient,
		store:         likes.NewStore(redisClient),
		snapshotGroup: "like-state-closure-snapshot-" + uuid.NewString(),
		behaviorGroup: "like-state-closure-behavior-" + uuid.NewString(),
	}
	originalDB, originalRedis, originalConfig := global.Db, global.RedisDB, config.AppConfig
	global.Db = db
	global.RedisDB = redisClient
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{
		LikeSnapshotGroupID: env.snapshotGroup,
		UserBehaviorGroupID: env.behaviorGroup,
		ActivityEventsTopic: "goexchange.activity.events.v1",
		LikeSnapshotTopic:   "goexchange.post.like.snapshot.v1",
		UserBehaviorTopic:   "goexchange.user.behavior.v1",
	}}
	t.Cleanup(func() {
		cleanupLikeStateClosureIntegration(env)
		global.Db, global.RedisDB, config.AppConfig = originalDB, originalRedis, originalConfig
		redisClient.Close()
	})
	return env
}

func cleanupLikeStateClosureIntegration(env *likeStateClosureIntegration) {
	if env == nil {
		return
	}
	for _, postID := range env.posts {
		postIDString := strconv.FormatUint(uint64(postID), 10)
		env.redis.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		env.redis.SRem(likes.DirtyKey, postID)
		env.redis.ZRem(likes.ProcessingKey, postIDString)
		env.redis.HDel(likes.ClaimsKey, postIDString)
		env.redis.SRem(likes.RegistryKey, postID)
		env.redis.ZRem(likes.ExpiryCandidatesKey, postIDString)
		env.redis.HDel(likes.RecoverableVersionsKey, postIDString)
		for _, userID := range env.users {
			pair := likes.BehaviorPair(userID, postID)
			env.redis.SRem(likes.BehaviorDirtyKey, pair)
			env.redis.HDel(likes.BehaviorStateKey, pair)
			env.redis.ZRem(likes.BehaviorProcessingKey, pair)
			env.redis.HDel(likes.BehaviorClaimsKey, pair)
		}
	}
	if env.db == nil {
		return
	}
	if len(env.posts) > 0 {
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostReaction{})
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostBehavior{})
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostArticle{})
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostRepost{})
		env.db.Unscoped().Where("id IN ?", env.posts).Delete(&models.Post{})
	}
	if len(env.users) > 0 {
		env.db.Unscoped().Where("user_id IN ?", env.users).Delete(&models.PostBehavior{})
		env.db.Unscoped().Where("user_id IN ?", env.users).Delete(&models.PostReaction{})
		env.db.Unscoped().Where("user_id IN ?", env.users).Delete(&models.UserRecoProfileDirty{})
		env.db.Unscoped().Where("id IN ?", env.users).Delete(&models.User{})
	}
	env.db.Unscoped().Where("consumer_name IN ?", []string{env.snapshotGroup, env.behaviorGroup}).Delete(&models.ConsumerInbox{})
	aggregates := make([]string, 0, len(env.posts)*len(env.users))
	for _, postID := range env.posts {
		for _, userID := range env.users {
			aggregates = append(aggregates, fmt.Sprintf("%d:%d", userID, postID))
		}
	}
	if len(aggregates) > 0 {
		env.db.Unscoped().Where("aggregate_id IN ?", aggregates).Delete(&models.OutboxEvent{})
	}
}

func invokeLikeStateClosureHandler(t *testing.T, method, path string, postID, userID uint, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(postID), 10)}}
	if userID != 0 {
		ctx.Set("user_id", userID)
	}
	handler(ctx)
	return recorder
}

func findLikeStateClosureEvent(events []eventing.Envelope, eventID string) (eventing.Envelope, bool) {
	for _, event := range events {
		if event.ID == eventID {
			return event, true
		}
	}
	return eventing.Envelope{}, false
}

func assertLikeStateClosureBehaviorQuiescent(t *testing.T, client *redis.Client, userID, postID uint) {
	t.Helper()
	pair := likes.BehaviorPair(userID, postID)
	if dirty, err := client.SIsMember(likes.BehaviorDirtyKey, pair).Result(); err != nil || dirty {
		t.Fatalf("behavior dirty=%t err=%v", dirty, err)
	}
	if exists, err := client.HExists(likes.BehaviorStateKey, pair).Result(); err != nil || exists {
		t.Fatalf("behavior state exists=%t err=%v", exists, err)
	}
	if _, err := client.ZScore(likes.BehaviorProcessingKey, pair).Result(); err != redis.Nil {
		t.Fatalf("behavior processing err=%v", err)
	}
	if exists, err := client.HExists(likes.BehaviorClaimsKey, pair).Result(); err != nil || exists {
		t.Fatalf("behavior claim exists=%t err=%v", exists, err)
	}
}

func assertLikeStateClosureRedisPurged(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	for _, key := range []string{likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("purged key=%q exists=%d err=%v", key, exists, err)
		}
	}
	postIDString := strconv.FormatUint(uint64(postID), 10)
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("purged dirty=%t err=%v", dirty, err)
	}
	if _, err := client.ZScore(likes.ProcessingKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("purged processing err=%v", err)
	}
	if exists, err := client.HExists(likes.ClaimsKey, postIDString).Result(); err != nil || exists {
		t.Fatalf("purged claim exists=%t err=%v", exists, err)
	}
	if registered, err := client.SIsMember(likes.RegistryKey, postID).Result(); err != nil || registered {
		t.Fatalf("purged registry=%t err=%v", registered, err)
	}
	if _, err := client.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("purged candidate err=%v", err)
	}
	if exists, err := client.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || exists {
		t.Fatalf("purged marker exists=%t err=%v", exists, err)
	}
}

func TestPostLikeSafeExpiryRecoveryIntegration(t *testing.T) {
	env := openLikeStateClosureIntegration(t)
	env.author = models.User{Username: "like-expiry-author-" + uuid.NewString(), Password: "test"}
	env.actor = models.User{Username: "like-expiry-actor-" + uuid.NewString(), Password: "test"}
	env.actor2 = models.User{Username: "like-expiry-actor-two-" + uuid.NewString(), Password: "test"}
	if err := env.db.Create(&[]*models.User{&env.author, &env.actor, &env.actor2}).Error; err != nil {
		t.Fatal(err)
	}
	env.users = []uint{env.author.ID, env.actor.ID, env.actor2.ID}
	env.post = models.Post{AuthorID: env.author.ID, Content: "safe expiry recovery", Visibility: "public"}
	if err := env.db.Create(&env.post).Error; err != nil {
		t.Fatal(err)
	}
	env.posts = []uint{env.post.ID}

	ctx := context.Background()
	if initialized, err := env.store.Initialize(ctx, env.post.ID, 0, 0, nil); err != nil || !initialized {
		t.Fatalf("initialize created=%t err=%v", initialized, err)
	}
	first := invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(env.post.ID), 10)+"/like", env.post.ID, env.actor.ID, controllers.LikePost)
	if first.Code != http.StatusOK {
		t.Fatalf("first like status=%d body=%s", first.Code, first.Body.String())
	}

	snapshotPublisher := &relayTestPublisher{}
	if err := runLikeSnapshotRelayBatch(ctx, env.store, snapshotPublisher); err != nil {
		t.Fatal(err)
	}
	snapshotEvent, ok := findLikeStateClosureEvent(snapshotPublisher.events, fmt.Sprintf("like-snapshot:%d:1", env.post.ID))
	if !ok {
		t.Fatalf("snapshot event missing events=%#v", snapshotPublisher.events)
	}
	if err := applyLikeSnapshotEvent(snapshotEvent); err != nil {
		t.Fatal(err)
	}

	behaviorPublisher := &relayTestPublisher{}
	if err := runLikeBehaviorRelayBatch(ctx, env.store, behaviorPublisher); err != nil {
		t.Fatal(err)
	}
	behaviorEvent, ok := findLikeStateClosureEvent(behaviorPublisher.events, fmt.Sprintf("like-state:%d:%d:1", env.actor.ID, env.post.ID))
	if !ok {
		t.Fatalf("behavior event missing events=%#v", behaviorPublisher.events)
	}
	if err := applyUserBehaviorEvent(behaviorEvent); err != nil {
		t.Fatal(err)
	}

	assertCanonicalLikeProjectionRows(t, env.db, env.post.ID, env.actor.ID, 1)
	state, err := env.store.LoadFullState(ctx, env.post.ID)
	if err != nil || state.Count != 1 || state.Version != 1 || len(state.UserIDs) != 1 || state.UserIDs[0] != env.actor.ID {
		t.Fatalf("durable Redis state=%+v err=%v", state, err)
	}
	if quiescent, err := env.store.SnapshotQueueQuiescent(ctx, env.post.ID); err != nil || !quiescent {
		t.Fatalf("snapshot queue quiescent=%t err=%v", quiescent, err)
	}
	assertLikeStateClosureBehaviorQuiescent(t, env.redis, env.actor.ID, env.post.ID)

	if armed, err := env.store.ArmExpiry(ctx, env.post.ID, 1, 2*time.Second); err != nil || !armed {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	postIDString := strconv.FormatUint(uint64(env.post.ID), 10)
	if marker, err := env.redis.HGet(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker != "1" {
		t.Fatalf("expiry marker=%q err=%v", marker, err)
	}
	if ttl, err := env.redis.TTL(likes.ReadyKey(env.post.ID)).Result(); err != nil || ttl <= 0 {
		t.Fatalf("Ready ttl=%s err=%v", ttl, err)
	}
	if registered, err := env.redis.SIsMember(likes.RegistryKey, env.post.ID).Result(); err != nil || !registered {
		t.Fatalf("registry=%t err=%v", registered, err)
	}
	if _, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("expiry candidate was not removed err=%v", err)
	}

	// Simulate expiry without touching the retained Registry or recovery marker.
	env.redis.Del(likes.ReadyKey(env.post.ID), likes.CountKey(env.post.ID), likes.UsersKey(env.post.ID), likes.VersionKey(env.post.ID))
	if registered, err := env.redis.SIsMember(likes.RegistryKey, env.post.ID).Result(); err != nil || !registered {
		t.Fatalf("registry after state deletion=%t err=%v", registered, err)
	}
	if marker, err := env.redis.HGet(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker != "1" {
		t.Fatalf("marker after state deletion=%q err=%v", marker, err)
	}

	second := invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+postIDString+"/like", env.post.ID, env.actor2.ID, controllers.LikePost)
	if second.Code != http.StatusOK {
		t.Fatalf("recovered like status=%d body=%s", second.Code, second.Body.String())
	}
	var secondPayload struct {
		Likes int64 `json:"likes"`
		Liked bool  `json:"liked"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if secondPayload.Likes != 2 || !secondPayload.Liked {
		t.Fatalf("recovered like payload=%#v", secondPayload)
	}

	state, err = env.store.LoadFullState(ctx, env.post.ID)
	if err != nil || state.Count != 2 || state.Version != 2 || len(state.UserIDs) != 2 || state.UserIDs[0] != env.actor.ID || state.UserIDs[1] != env.actor2.ID {
		t.Fatalf("recovered Redis state=%+v err=%v", state, err)
	}
	if marker, err := env.redis.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker {
		t.Fatalf("marker after recovery exists=%t err=%v", marker, err)
	}
	if registered, err := env.redis.SIsMember(likes.RegistryKey, env.post.ID).Result(); err != nil || !registered {
		t.Fatalf("registry after recovery=%t err=%v", registered, err)
	}
	if _, err := env.redis.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != nil {
		t.Fatalf("refreshed expiry candidate err=%v", err)
	}
	for _, key := range []string{likes.ReadyKey(env.post.ID), likes.CountKey(env.post.ID), likes.UsersKey(env.post.ID), likes.VersionKey(env.post.ID)} {
		if ttl, err := env.redis.TTL(key).Result(); err != nil || ttl != -1 {
			t.Fatalf("persistent key=%q ttl=%s err=%v", key, ttl, err)
		}
	}

	secondSnapshotPublisher := &relayTestPublisher{}
	if err := runLikeSnapshotRelayBatch(ctx, env.store, secondSnapshotPublisher); err != nil {
		t.Fatal(err)
	}
	secondSnapshot, ok := findLikeStateClosureEvent(secondSnapshotPublisher.events, fmt.Sprintf("like-snapshot:%d:2", env.post.ID))
	if !ok {
		t.Fatalf("second snapshot event missing events=%#v", secondSnapshotPublisher.events)
	}
	if err := applyLikeSnapshotEvent(secondSnapshot); err != nil {
		t.Fatal(err)
	}
	secondBehaviorPublisher := &relayTestPublisher{}
	if err := runLikeBehaviorRelayBatch(ctx, env.store, secondBehaviorPublisher); err != nil {
		t.Fatal(err)
	}
	secondBehavior, ok := findLikeStateClosureEvent(secondBehaviorPublisher.events, fmt.Sprintf("like-state:%d:%d:2", env.actor2.ID, env.post.ID))
	if !ok {
		t.Fatalf("second behavior event missing events=%#v", secondBehaviorPublisher.events)
	}
	if err := applyUserBehaviorEvent(secondBehavior); err != nil {
		t.Fatal(err)
	}

	var projectedPost models.Post
	if err := env.db.First(&projectedPost, env.post.ID).Error; err != nil {
		t.Fatal(err)
	}
	if projectedPost.LikeCount != 2 || projectedPost.LikeSyncVersion != 2 {
		t.Fatalf("post after recovered projection=%#v", projectedPost)
	}
	for _, userID := range []uint{env.actor.ID, env.actor2.ID} {
		var reaction models.PostReaction
		if err := env.db.Where("user_id = ? AND post_id = ?", userID, env.post.ID).First(&reaction).Error; err != nil {
			t.Fatal(err)
		}
		wantVersion := int64(1)
		if userID == env.actor2.ID {
			wantVersion = 2
		}
		if !reaction.Liked || reaction.Version != wantVersion {
			t.Fatalf("reaction user=%d=%#v", userID, reaction)
		}
	}
	if quiescent, err := env.store.SnapshotQueueQuiescent(ctx, env.post.ID); err != nil || !quiescent {
		t.Fatalf("recovered snapshot queue quiescent=%t err=%v", quiescent, err)
	}
	assertLikeStateClosureBehaviorQuiescent(t, env.redis, env.actor2.ID, env.post.ID)
}

func TestPostLikeDeletePurgeFailureReconcilesIntegration(t *testing.T) {
	env := openLikeStateClosureIntegration(t)
	env.author = models.User{Username: "like-delete-author-" + uuid.NewString(), Password: "test"}
	if err := env.db.Create(&env.author).Error; err != nil {
		t.Fatal(err)
	}
	env.users = []uint{env.author.ID}
	env.post = models.Post{AuthorID: env.author.ID, Content: "delete reconciliation", Visibility: "public"}
	if err := env.db.Create(&env.post).Error; err != nil {
		t.Fatal(err)
	}
	env.posts = []uint{env.post.ID}
	if initialized, err := env.store.Initialize(t.Context(), env.post.ID, 0, 0, nil); err != nil || !initialized {
		t.Fatalf("initialize created=%t err=%v", initialized, err)
	}
	likeRecorder := invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(env.post.ID), 10)+"/like", env.post.ID, env.author.ID, controllers.LikePost)
	if likeRecorder.Code != http.StatusOK {
		t.Fatalf("like status=%d body=%s", likeRecorder.Code, likeRecorder.Body.String())
	}
	now := time.Now().UTC()
	if err := env.db.Create(&models.PostReaction{
		UserID: env.author.ID, PostID: env.post.ID, Reaction: models.PostReactionLike,
		Liked: true, Version: 1, UpdatedAt: now, StateChangedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	validRedis := env.redis
	invalidRedis := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	})
	global.RedisDB = invalidRedis
	deleteRecorder := invokeLikeStateClosureHandler(t, http.MethodDelete, "/api/posts/"+strconv.FormatUint(uint64(env.post.ID), 10), env.post.ID, env.author.ID, controllers.DeletePost)
	global.RedisDB = validRedis
	invalidRedis.Close()
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	var deletedPost models.Post
	if err := env.db.Unscoped().First(&deletedPost, env.post.ID).Error; err != nil || !deletedPost.DeletedAt.Valid {
		t.Fatalf("soft deleted post=%#v err=%v", deletedPost, err)
	}
	if exists, err := env.redis.Exists(likes.ReadyKey(env.post.ID)).Result(); err != nil || exists != 1 {
		t.Fatalf("residual Ready exists=%d err=%v", exists, err)
	}
	if registered, err := env.redis.SIsMember(likes.RegistryKey, env.post.ID).Result(); err != nil || !registered {
		t.Fatalf("residual registry=%t err=%v", registered, err)
	}
	if dirty, err := env.redis.SIsMember(likes.DirtyKey, env.post.ID).Result(); err != nil || !dirty {
		t.Fatalf("residual snapshot dirty=%t err=%v", dirty, err)
	}

	if _, err := runLikeStateMaintenancePass(t.Context(), env.store, env.db, 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertLikeStateClosureRedisPurged(t, env.redis, env.post.ID)
	var reactionCount int64
	if err := env.db.Model(&models.PostReaction{}).Where("user_id = ? AND post_id = ?", env.author.ID, env.post.ID).Count(&reactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if reactionCount != 1 {
		t.Fatalf("PostReaction count=%d want 1", reactionCount)
	}
}

func TestLikeStateMaintenanceRegistryLeavesActiveAndPurgesDeletedOrMissingIntegration(t *testing.T) {
	env := openLikeStateClosureIntegration(t)
	env.author = models.User{Username: "like-maintenance-author-" + uuid.NewString(), Password: "test"}
	if err := env.db.Create(&env.author).Error; err != nil {
		t.Fatal(err)
	}
	env.users = []uint{env.author.ID}
	posts := []models.Post{
		{AuthorID: env.author.ID, Content: "maintenance active", Visibility: "public"},
		{AuthorID: env.author.ID, Content: "maintenance soft deleted", Visibility: "public"},
		{AuthorID: env.author.ID, Content: "maintenance missing", Visibility: "public"},
	}
	if err := env.db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		env.posts = append(env.posts, post.ID)
		if initialized, err := env.store.Initialize(t.Context(), post.ID, 0, 0, nil); err != nil || !initialized {
			t.Fatalf("post=%d initialize created=%t err=%v", post.ID, initialized, err)
		}
	}
	if err := env.db.Delete(&posts[1]).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Unscoped().Where("id = ?", posts[2].ID).Delete(&models.Post{}).Error; err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	var cursor uint64
	for {
		next, err := reconcileLikeStateRegistry(ctx, env.store, env.db, cursor, 1000)
		if err != nil {
			t.Fatal(err)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if registered, err := env.redis.SIsMember(likes.RegistryKey, posts[0].ID).Result(); err != nil || !registered {
		t.Fatalf("active registry=%t err=%v", registered, err)
	}
	if exists, err := env.redis.Exists(likes.ReadyKey(posts[0].ID)).Result(); err != nil || exists != 1 {
		t.Fatalf("active Ready exists=%d err=%v", exists, err)
	}
	assertLikeStateClosureRedisPurged(t, env.redis, posts[1].ID)
	assertLikeStateClosureRedisPurged(t, env.redis, posts[2].ID)
}
