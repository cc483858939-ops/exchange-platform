package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestPostLikeHotPathDoesNotLoadPostgres(t *testing.T) {
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	redisClient := openPostLikeIntegrationRedis(t)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1
	cleanupPostLikeIntegrationState(redisClient, []uint{postID}, []uint{userID})
	t.Cleanup(func() {
		cleanupPostLikeIntegrationState(redisClient, []uint{postID}, []uint{userID})
	})

	originalBaselineLoader := loadPostLikeBaselineFromDB
	originalBatchBaselineLoader := loadPostLikeBaselinesFromDB
	baselineCalls := 0
	batchBaselineCalls := 0
	t.Cleanup(func() {
		loadPostLikeBaselineFromDB = originalBaselineLoader
		loadPostLikeBaselinesFromDB = originalBatchBaselineLoader
	})
	loadPostLikeBaselineFromDB = func(uint) (postLikeBaseline, error) {
		baselineCalls++
		return postLikeBaseline{}, errors.New("unexpected PostgreSQL baseline load")
	}
	loadPostLikeBaselinesFromDB = func([]uint) (map[uint]postLikeBaseline, error) {
		batchBaselineCalls++
		return nil, errors.New("unexpected PostgreSQL batch baseline load")
	}

	store := likes.NewStore(redisClient)
	if created, err := store.Initialize(t.Context(), postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	assertPostLikeMutationIntegration(t, postID, userID, true, 1, true)
	assertPostLikeMutationIntegration(t, postID, userID, false, 0, false)
	assertPostLikeStateIntegration(t, postID, userID, 0, false)

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts/like-states",
		"",
		fmt.Sprintf(`{"post_ids":[%d]}`, postID),
		userID,
	)
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("hot bulk state status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postLikeStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].PostID != postID || response.Items[0].Likes != 0 || response.Items[0].Liked || len(response.UnavailablePostIDs) != 0 {
		t.Fatalf("hot bulk state response=%#v", response)
	}
	if baselineCalls != 0 || batchBaselineCalls != 0 {
		t.Fatalf("PostgreSQL loaders called baseline=%d batch=%d", baselineCalls, batchBaselineCalls)
	}
}

func TestPostLikeStatesProjectionNotReadyIsPerIDUnavailable(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostReaction{}); err != nil {
		t.Fatal(err)
	}
	redisClient := openPostLikeIntegrationRedis(t)

	viewer := models.User{Username: "like-batch-viewer-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := make([]uint, 0, 3)
	userIDs := []uint{viewer.ID}
	t.Cleanup(func() {
		cleanupPostLikeIntegrationState(redisClient, postIDs, userIDs)
		if len(postIDs) > 0 {
			db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		db.Unscoped().Where("id = ?", viewer.ID).Delete(&models.User{})
	})

	posts := []models.Post{
		{AuthorID: viewer.ID, Content: "batch hot", Visibility: "public"},
		{AuthorID: viewer.ID, Content: "batch cold safe", Visibility: "public"},
		{AuthorID: viewer.ID, Content: "batch projection mismatch", Visibility: "public", LikeCount: 1, LikeSyncVersion: 1},
	}
	if err := db.Create(&posts).Error; err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}

	store := likes.NewStore(redisClient)
	if created, err := store.Initialize(t.Context(), posts[0].ID, 0, 0, nil); err != nil || !created {
		t.Fatalf("hot initialize created=%t err=%v", created, err)
	}

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts/like-states",
		"",
		fmt.Sprintf(`{"post_ids":[%d,%d,%d]}`, posts[0].ID, posts[1].ID, posts[2].ID),
		viewer.ID,
	)
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("batch state status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postLikeStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || len(response.UnavailablePostIDs) != 1 || response.UnavailablePostIDs[0] != posts[2].ID {
		t.Fatalf("batch response=%#v", response)
	}
	items := make(map[uint]postLikeStateItem, len(response.Items))
	for _, item := range response.Items {
		items[item.PostID] = item
	}
	for _, postID := range posts[:2] {
		item, ok := items[postID.ID]
		if !ok || item.Likes != 0 || item.Liked {
			t.Fatalf("post=%d item=%#v exists=%t", postID.ID, item, ok)
		}
	}
	for _, key := range []string{likes.ReadyKey(posts[2].ID), likes.CountKey(posts[2].ID), likes.UsersKey(posts[2].ID), likes.VersionKey(posts[2].ID)} {
		if exists, err := redisClient.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("projection-mismatch key=%q exists=%d err=%v", key, exists, err)
		}
	}
	postIDString := strconv.FormatUint(uint64(posts[2].ID), 10)
	if registered, err := redisClient.SIsMember(likes.RegistryKey, posts[2].ID).Result(); err != nil || registered {
		t.Fatalf("projection-mismatch registry=%t err=%v", registered, err)
	}
	if _, err := redisClient.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("projection-mismatch candidate err=%v", err)
	}
	if marker, err := redisClient.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || marker {
		t.Fatalf("projection-mismatch marker=%t err=%v", marker, err)
	}
}
