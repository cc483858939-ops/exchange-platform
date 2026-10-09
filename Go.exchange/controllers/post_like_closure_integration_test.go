package controllers

import (
	"context"
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
	if err := cleanupPostLikeIntegrationState(redisClient, []uint{postID}, []uint{userID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanupPostLikeIntegrationState(redisClient, []uint{postID}, []uint{userID}); err != nil {
			t.Errorf("cleanup post-like Redis integration state: %v", err)
		}
	})

	originalBaselineLoader := loadPostLikeBaselineFromDB
	originalBatchBaselineLoader := loadPostLikeBaselinesFromDB
	baselineCalls := 0
	batchBaselineCalls := 0
	t.Cleanup(func() {
		loadPostLikeBaselineFromDB = originalBaselineLoader
		loadPostLikeBaselinesFromDB = originalBatchBaselineLoader
	})
	loadPostLikeBaselineFromDB = func(context.Context, uint) (postLikeBaseline, error) {
		baselineCalls++
		return postLikeBaseline{}, errors.New("unexpected PostgreSQL baseline load")
	}
	loadPostLikeBaselinesFromDB = func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
		batchBaselineCalls++
		return nil, errors.New("unexpected PostgreSQL batch baseline load")
	}

	store := likes.NewStore(redisClient)
	if created, err := initializeLikeStore(store, t.Context(), postID, 0, 0, nil); err != nil || !created {
		t.Fatalf("initialize created=%t err=%v", created, err)
	}
	if err := store.InitializeUserEmpty(t.Context(), userID); err != nil {
		t.Fatal(err)
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
		if err := cleanupPostLikeIntegrationState(redisClient, postIDs, userIDs); err != nil {
			t.Errorf("cleanup post-like Redis integration state: %v", err)
		}
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
	oldSingleBaselineLoader, oldBatchBaselineLoader := loadPostLikeBaselineFromDB, loadPostLikeBaselinesFromDB
	baselineCalls, batchBaselineCalls := 0, 0
	loadPostLikeBaselineFromDB = func(context.Context, uint) (postLikeBaseline, error) {
		baselineCalls++
		return postLikeBaseline{}, errors.New("unexpected per-Post SQL baseline load")
	}
	loadPostLikeBaselinesFromDB = func(context.Context, []uint) (map[uint]postLikeBaseline, error) {
		batchBaselineCalls++
		return nil, errors.New("unexpected SQL baseline batch load")
	}
	t.Cleanup(func() {
		loadPostLikeBaselineFromDB = oldSingleBaselineLoader
		loadPostLikeBaselinesFromDB = oldBatchBaselineLoader
	})
	if err := store.InitializeUserEmpty(t.Context(), viewer.ID); err != nil {
		t.Fatal(err)
	}
	if created, err := initializeLikeStore(store, t.Context(), posts[0].ID, 0, 0, nil); err != nil || !created {
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
	if len(response.Items) != 1 || len(response.UnavailablePostIDs) != 2 {
		t.Fatalf("batch response=%#v", response)
	}
	unavailable := make(map[uint]struct{}, len(response.UnavailablePostIDs))
	for _, postID := range response.UnavailablePostIDs {
		unavailable[postID] = struct{}{}
	}
	if _, ok := unavailable[posts[1].ID]; !ok {
		t.Fatalf("cold zero-state Post was not reported unavailable: %#v", response.UnavailablePostIDs)
	}
	if _, ok := unavailable[posts[2].ID]; !ok {
		t.Fatalf("projection-mismatch Post was not reported unavailable: %#v", response.UnavailablePostIDs)
	}
	items := make(map[uint]postLikeStateItem, len(response.Items))
	for _, item := range response.Items {
		items[item.PostID] = item
	}
	if item, ok := items[posts[0].ID]; !ok || item.Likes != 0 || item.Liked {
		t.Fatalf("ready post=%d item=%#v exists=%t", posts[0].ID, item, ok)
	}
	if baselineCalls != 0 || batchBaselineCalls != 0 {
		t.Fatalf("batch read performed SQL recovery loads: single=%d batch=%d", baselineCalls, batchBaselineCalls)
	}
	for _, unavailablePost := range posts[1:] {
		for _, key := range []string{likes.ReadyKey(unavailablePost.ID), likes.CountKey(unavailablePost.ID), likes.UsersKey(unavailablePost.ID), likes.VersionKey(unavailablePost.ID)} {
			if exists, err := redisClient.Exists(key).Result(); err != nil || exists != 0 {
				t.Fatalf("unavailable Post key=%q exists=%d err=%v", key, exists, err)
			}
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
