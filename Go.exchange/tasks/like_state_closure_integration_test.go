package tasks

import (
	"context"
	"errors"
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
	"Go.exchange/internal/testdb"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
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

	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Post{},
		&models.PostLikeCleanup{},
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
	originalAPIDB := global.APIDb
	originalWorkerDB := global.WorkerDb
	originalRedis := global.RedisDB
	originalConfig := config.AppConfig
	global.APIDb = db
	global.WorkerDb = db
	global.RedisDB = redisClient
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{
		LikeSnapshotGroupID: env.snapshotGroup,
		UserBehaviorGroupID: env.behaviorGroup,
		ActivityEventsTopic: "goexchange.activity.events.v1",
		LikeSnapshotTopic:   "goexchange.post.like.snapshot.v1",
		UserBehaviorTopic:   "goexchange.user.behavior.v1",
	}}
	t.Cleanup(func() {
		if err := cleanupLikeStateClosureIntegration(env); err != nil {
			t.Errorf("cleanup Like state closure integration: %v", err)
		}
		global.APIDb = originalAPIDB
		global.WorkerDb = originalWorkerDB
		global.RedisDB = originalRedis
		config.AppConfig = originalConfig
		if err := redisClient.Close(); err != nil {
			t.Errorf("close Redis integration client: %v", err)
		}
	})
	return env
}

func cleanupLikeStateClosureIntegration(env *likeStateClosureIntegration) error {
	if env == nil {
		return nil
	}
	var cleanupErr error
	if env.redis != nil {
		cleanupErr = cleanupLikeRelayIntegrationState(env.redis, env.posts, env.users)
	}
	if env.db == nil {
		return cleanupErr
	}
	if len(env.posts) > 0 {
		env.db.Where("post_id IN ?", env.posts).Delete(&models.PostLikeCleanup{})
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostReaction{})
		env.db.Unscoped().Where("post_id IN ?", env.posts).Delete(&models.PostBehavior{})
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
	return cleanupErr
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

func assertLikeStateClosureRedisDeleted(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	if ready, err := client.Get(likes.ReadyKey(postID)).Result(); err != nil || ready != "deleted" {
		t.Fatalf("deleted fence=%q err=%v", ready, err)
	}
	store := likes.NewStore(client)
	if _, err := store.Get(t.Context(), 1, postID); !errors.Is(err, likes.ErrPostLikeUnavailable) {
		t.Fatalf("deleted state read=%v", err)
	}
	if _, err := store.Mutate(t.Context(), 1, postID, true); !errors.Is(err, likes.ErrPostLikeUnavailable) {
		t.Fatalf("deleted state mutation=%v", err)
	}
	for _, key := range []string{likes.CountKey(postID), likes.VersionKey(postID)} {
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

func TestPostLikeRelaysPreservedAndNonzeroRecoveryFailsClosedIntegration(t *testing.T) {
	t.Setenv("USER_LIKE_TTL_ARMING_ENABLED", "true")
	t.Setenv("USER_LIKE_SET_TTL", "72h")
	env := openLikeStateClosureIntegration(t)
	env.author = models.User{Username: "like-relay-author-" + uuid.NewString(), Password: "test"}
	env.actor = models.User{Username: "like-relay-actor-" + uuid.NewString(), Password: "test"}
	env.actor2 = models.User{Username: "like-relay-actor-two-" + uuid.NewString(), Password: "test"}
	if err := env.db.Create(&[]*models.User{&env.author, &env.actor, &env.actor2}).Error; err != nil {
		t.Fatal(err)
	}
	env.users = []uint{env.author.ID, env.actor.ID, env.actor2.ID}
	env.post = models.Post{AuthorID: env.author.ID, Content: "User to Posts relays", Visibility: "public"}
	if err := env.db.Create(&env.post).Error; err != nil {
		t.Fatal(err)
	}
	env.posts = []uint{env.post.ID}
	ctx := context.Background()
	if initialized, err := initializeLikeStore(env.store, ctx, env.post.ID, 0, 0, nil); err != nil || !initialized {
		t.Fatalf("initialize created=%t err=%v", initialized, err)
	}
	for _, userID := range []uint{env.actor.ID, env.actor2.ID} {
		if err := env.store.InitializeUserEmpty(ctx, userID); err != nil {
			t.Fatal(err)
		}
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
	if _, err := applyLikeSnapshotEvent(ctx, env.db, snapshotEvent); err != nil {
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
	if err := applyUserBehaviorEventForIntegration(t, env.db, config.AppConfig.Kafka, behaviorEvent); err != nil {
		t.Fatal(err)
	}
	assertCanonicalLikeProjectionRows(t, env.db, env.post.ID, env.actor.ID, 1)
	state, err := env.store.LoadFullState(ctx, env.post.ID)
	if err != nil || state.Count != 1 || state.Version != 1 {
		t.Fatalf("aggregate Redis state=%+v err=%v", state, err)
	}
	if initialized, err := env.redis.SIsMember(likes.UserLikesKey(env.actor.ID), likes.UserLikesInitSentinel).Result(); err != nil || !initialized {
		t.Fatalf("actor sentinel=%t err=%v", initialized, err)
	}
	if liked, err := env.redis.SIsMember(likes.UserLikesKey(env.actor.ID), strconv.FormatUint(uint64(env.post.ID), 10)).Result(); err != nil || !liked {
		t.Fatalf("actor relation=%t err=%v", liked, err)
	}
	if exists, err := env.redis.Exists(likes.UsersKey(env.post.ID)).Result(); err != nil || exists != 0 {
		t.Fatalf("legacy Post Users key exists=%d err=%v", exists, err)
	}
	if armed, err := env.store.ArmExpiry(ctx, env.post.ID, 1, time.Second); armed || !errors.Is(err, likes.ErrLikeStateExpiryUnsupported) {
		t.Fatalf("ArmExpiry armed=%t err=%v", armed, err)
	}
	for _, key := range []string{likes.ReadyKey(env.post.ID), likes.CountKey(env.post.ID), likes.VersionKey(env.post.ID)} {
		if ttl, err := env.redis.TTL(key).Result(); err != nil || ttl != -1 {
			t.Fatalf("key=%q TTL=%s err=%v want persistent", key, ttl, err)
		}
	}
	assertLikeStateClosureUserLikeTTL(t, env.redis, env.actor.ID)

	// Simulated loss of Post aggregates cannot rebuild a nonzero user relation
	// from the SQL PostReaction projection in SPEC-01.
	if err := env.redis.Del(likes.ReadyKey(env.post.ID), likes.CountKey(env.post.ID), likes.VersionKey(env.post.ID)).Err(); err != nil {
		t.Fatal(err)
	}
	second := invokeLikeStateClosureHandler(t, http.MethodPut, "/api/posts/"+strconv.FormatUint(uint64(env.post.ID), 10)+"/like", env.post.ID, env.actor2.ID, controllers.LikePost)
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("nonzero recovery status=%d body=%s want 503", second.Code, second.Body.String())
	}
	if exists, err := env.redis.Exists(likes.ReadyKey(env.post.ID), likes.CountKey(env.post.ID), likes.VersionKey(env.post.ID)).Result(); err != nil || exists != 0 {
		t.Fatalf("unsafe recovery partially restored aggregate keys: exists=%d err=%v", exists, err)
	}
}

func assertLikeStateClosureUserLikeTTL(t *testing.T, client *redis.Client, userID uint) {
	t.Helper()
	result, err := client.Eval(`
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
return {
  tostring(redis.call('PTTL', KEYS[1])),
  tostring(redis.call('PTTL', KEYS[2])),
  redis.call('HGET', KEYS[3], ARGV[1]) or '',
  tostring(now_ms),
  tostring(redis.call('SCARD', KEYS[1]) - 1),
  tostring(redis.call('ZCARD', KEYS[2]))
}
`, []string{likes.UserLikesKey(userID), likes.UserLikesOrderKey(userID), likes.UserLikesExpiryLedgerKey}, strconv.FormatUint(uint64(userID), 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	items, ok := result.([]interface{})
	if !ok || len(items) != 6 {
		t.Fatalf("unexpected User Like TTL result %T", result)
	}
	values := make([]int64, len(items))
	for index, item := range items {
		values[index], err = strconv.ParseInt(fmt.Sprint(item), 10, 64)
		if err != nil {
			t.Fatalf("invalid User Like TTL result item %d=%v: %v", index, item, err)
		}
	}
	setTTL, orderTTL, expiresAt, nowMS, activeRelations, orderMembers := values[0], values[1], values[2], values[3], values[4], values[5]
	wantTTL := int64((72 * time.Hour).Milliseconds())
	if setTTL <= 0 || setTTL > wantTTL || setTTL < wantTTL-int64(time.Minute.Milliseconds()) {
		t.Fatalf("User Set PTTL=%dms, want active and close to 72h", setTTL)
	}
	ttlDelta := setTTL - orderTTL
	if ttlDelta < 0 {
		ttlDelta = -ttlDelta
	}
	if activeRelations != 1 || orderMembers != activeRelations || orderTTL <= 0 || ttlDelta > 1 {
		t.Fatalf("User Set/Order state diverged: set_ttl=%d order_ttl=%d active=%d order_members=%d", setTTL, orderTTL, activeRelations, orderMembers)
	}
	if delta := nowMS + setTTL - expiresAt; delta < -2 || delta > 2 {
		t.Fatalf("User Like expiry ledger differs from Redis deadline by %dms", delta)
	}
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
	if initialized, err := initializeLikeStore(env.store, t.Context(), env.post.ID, 0, 0, nil); err != nil || !initialized {
		t.Fatalf("initialize created=%t err=%v", initialized, err)
	}
	if err := env.store.InitializeUserEmpty(t.Context(), env.author.ID); err != nil {
		t.Fatal(err)
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
	if liked, err := env.redis.SIsMember(likes.UserLikesKey(env.author.ID), strconv.FormatUint(uint64(env.post.ID), 10)).Result(); err != nil || !liked {
		t.Fatalf("Post purge unexpectedly removed User relation liked=%t err=%v", liked, err)
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
	assertLikeStateClosureRedisDeleted(t, env.redis, env.post.ID)
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
		if initialized, err := initializeLikeStore(env.store, t.Context(), post.ID, 0, 0, nil); err != nil || !initialized {
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
	assertLikeStateClosureRedisDeleted(t, env.redis, posts[1].ID)
	assertLikeStateClosureRedisDeleted(t, env.redis, posts[2].ID)
}
