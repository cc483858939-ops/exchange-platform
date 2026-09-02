package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
)

func TestPostCreationFormsAreImmediatelyLikeReadyIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostReaction{}, &models.UserRecoProfileDirty{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	fixture := newReplyIntegrationFixture(t, db)
	redisClient := openPostLikeIntegrationRedis(t)

	createdIDs := make([]uint, 0, 6)
	userIDs := []uint{fixture.Author.ID, fixture.Commenter.ID, fixture.Other.ID}
	t.Cleanup(func() {
		if err := cleanupPostLikeIntegrationState(redisClient, createdIDs, userIDs); err != nil {
			t.Errorf("cleanup post-like Redis integration state: %v", err)
		}
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostArticle{})
		db.Unscoped().Where("post_id IN ? OR user_id IN ?", createdIDs, userIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfileDirty{})
		for index := len(createdIDs) - 1; index >= 0; index-- {
			db.Unscoped().Where("id = ?", createdIDs[index]).Delete(&models.Post{})
		}
	})

	shortRoot := createPostForLikeIntegration(t, fixture.Author.ID, `{"content":"short root"}`)
	createdIDs = append(createdIDs, shortRoot.ID)
	assertPostLikeStateIntegration(t, shortRoot.ID, fixture.Commenter.ID, 0, false)

	articleRoot := createPostForLikeIntegration(t, fixture.Author.ID, `{"content":"article root","article":{"title":"Article root","preview":"Article preview"}}`)
	createdIDs = append(createdIDs, articleRoot.ID)
	if articleRoot.Article == nil {
		t.Fatalf("article root response=%#v", articleRoot)
	}
	assertPostLikeStateIntegration(t, articleRoot.ID, fixture.Commenter.ID, 0, false)

	genericReply := createPostForLikeIntegration(t, fixture.Commenter.ID, `{"content":"generic reply","reply_to_post_id":`+strconvUint(articleRoot.ID)+`}`)
	createdIDs = append(createdIDs, genericReply.ID)
	assertPostLikeStateIntegration(t, genericReply.ID, fixture.Other.ID, 0, false)

	dedicatedReply := createCanonicalReplyForLikeIntegration(t, fixture.Commenter.ID, shortRoot.ID, "canonical reply")
	createdIDs = append(createdIDs, dedicatedReply.ID)
	assertPostLikeStateIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, 0, false)

	nestedReply := createCanonicalReplyForLikeIntegration(t, fixture.Other.ID, dedicatedReply.ID, "nested reply")
	createdIDs = append(createdIDs, nestedReply.ID)
	assertPostLikeStateIntegration(t, nestedReply.ID, fixture.Other.ID, 0, false)

	quotePost := createPostForLikeIntegration(t, fixture.Other.ID, `{"content":"quote","quote_post_id":`+strconvUint(shortRoot.ID)+`}`)
	createdIDs = append(createdIDs, quotePost.ID)
	assertPostLikeStateIntegration(t, quotePost.ID, fixture.Author.ID, 0, false)

	if dedicatedReply.ReplyToPostID == nil || *dedicatedReply.ReplyToPostID != shortRoot.ID || dedicatedReply.ConversationID != shortRoot.ID {
		t.Fatalf("dedicated reply graph=%#v", dedicatedReply)
	}
	if nestedReply.ReplyToPostID == nil || *nestedReply.ReplyToPostID != dedicatedReply.ID || nestedReply.ConversationID != shortRoot.ID {
		t.Fatalf("nested reply graph=%#v", nestedReply)
	}
	var shortRootRow, dedicatedReplyRow models.Post
	if err := db.First(&shortRootRow, shortRoot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&dedicatedReplyRow, dedicatedReply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if shortRootRow.ReplyCount != 1 || dedicatedReplyRow.ReplyCount != 1 {
		t.Fatalf("reply counts root=%d dedicated=%d", shortRootRow.ReplyCount, dedicatedReplyRow.ReplyCount)
	}

	assertPostLikeMutationIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, true, 1, true)
	assertPostLikeStateIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, 1, true)
	assertPostLikeMutationIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, true, 1, true)
	assertPostLikeMutationIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, false, 0, false)
	assertPostLikeMutationIntegration(t, dedicatedReply.ID, fixture.Commenter.ID, false, 0, false)
	assertPostLikeStateIntegration(t, shortRoot.ID, fixture.Commenter.ID, 0, false)
	assertPostLikeStatesIntegration(t, []uint{shortRoot.ID, dedicatedReply.ID, nestedReply.ID}, fixture.Commenter.ID)
}

func openPostLikeIntegrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	dbNumber, _ := strconv.Atoi(os.Getenv("REDIS_TEST_DB"))
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_TEST_ADDR"), DB: dbNumber})
	if err := client.Ping().Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	previousRedis := global.RedisDB
	global.RedisDB = client
	t.Cleanup(func() {
		global.RedisDB = previousRedis
		client.Close()
	})
	return client
}

func cleanupPostLikeIntegrationState(client *redis.Client, postIDs, userIDs []uint) error {
	if client == nil {
		return nil
	}
	if len(postIDs) == 0 {
		return nil
	}
	pipe := client.Pipeline()
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		postIDString := strconv.FormatUint(uint64(postID), 10)
		pipe.Del(likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID))
		pipe.SRem(likes.DirtyKey, postID)
		pipe.ZRem(likes.ProcessingKey, postIDString)
		pipe.HDel(likes.ClaimsKey, postIDString)
		pipe.SRem(likes.RegistryKey, postID)
		pipe.ZRem(likes.ExpiryCandidatesKey, postIDString)
		pipe.HDel(likes.RecoverableVersionsKey, postIDString)
		for _, userID := range userIDs {
			if userID == 0 {
				continue
			}
			pair := likes.BehaviorPair(userID, postID)
			pipe.SRem(likes.BehaviorDirtyKey, pair)
			pipe.HDel(likes.BehaviorStateKey, pair)
			pipe.ZRem(likes.BehaviorProcessingKey, pair)
			pipe.HDel(likes.BehaviorClaimsKey, pair)
		}
	}
	_, err := pipe.Exec()
	return err
}

func TestCleanupPostLikeIntegrationStateRemovesOwnedRedisMetadata(t *testing.T) {
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	client := openPostLikeIntegrationRedis(t)
	postID := uint(time.Now().UnixNano() & 0x3fffffff)
	userID := postID + 1
	postIDString := strconv.FormatUint(uint64(postID), 10)
	pair := likes.BehaviorPair(userID, postID)
	t.Cleanup(func() {
		if err := cleanupPostLikeIntegrationState(client, []uint{postID}, []uint{userID}); err != nil {
			t.Errorf("backup cleanup post-like Redis integration state: %v", err)
		}
	})

	pipe := client.Pipeline()
	pipe.Set(likes.ReadyKey(postID), "1", 0)
	pipe.Set(likes.CountKey(postID), "1", 0)
	pipe.SAdd(likes.UsersKey(postID), userID)
	pipe.Set(likes.VersionKey(postID), "1", 0)
	pipe.SAdd(likes.DirtyKey, postID)
	pipe.ZAdd(likes.ProcessingKey, &redis.Z{Score: 1, Member: postIDString})
	pipe.HSet(likes.ClaimsKey, postIDString, "claim")
	pipe.SAdd(likes.RegistryKey, postID)
	pipe.ZAdd(likes.ExpiryCandidatesKey, &redis.Z{Score: 1, Member: postIDString})
	pipe.HSet(likes.RecoverableVersionsKey, postIDString, "1")
	pipe.SAdd(likes.BehaviorDirtyKey, pair)
	pipe.HSet(likes.BehaviorStateKey, pair, "state")
	pipe.ZAdd(likes.BehaviorProcessingKey, &redis.Z{Score: 1, Member: pair})
	pipe.HSet(likes.BehaviorClaimsKey, pair, "claim")
	if _, err := pipe.Exec(); err != nil {
		t.Fatal(err)
	}

	if err := cleanupPostLikeIntegrationState(client, []uint{postID}, []uint{userID}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("owned key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("dirty membership=%t err=%v", dirty, err)
	}
	if _, err := client.ZScore(likes.ProcessingKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("processing membership err=%v", err)
	}
	if exists, err := client.HExists(likes.ClaimsKey, postIDString).Result(); err != nil || exists {
		t.Fatalf("claims membership=%t err=%v", exists, err)
	}
	if registered, err := client.SIsMember(likes.RegistryKey, postID).Result(); err != nil || registered {
		t.Fatalf("registry membership=%t err=%v", registered, err)
	}
	if _, err := client.ZScore(likes.ExpiryCandidatesKey, postIDString).Result(); err != redis.Nil {
		t.Fatalf("expiry candidate membership err=%v", err)
	}
	if exists, err := client.HExists(likes.RecoverableVersionsKey, postIDString).Result(); err != nil || exists {
		t.Fatalf("recoverable marker membership=%t err=%v", exists, err)
	}
	if dirty, err := client.SIsMember(likes.BehaviorDirtyKey, pair).Result(); err != nil || dirty {
		t.Fatalf("behavior dirty membership=%t err=%v", dirty, err)
	}
	if exists, err := client.HExists(likes.BehaviorStateKey, pair).Result(); err != nil || exists {
		t.Fatalf("behavior state membership=%t err=%v", exists, err)
	}
	if _, err := client.ZScore(likes.BehaviorProcessingKey, pair).Result(); err != redis.Nil {
		t.Fatalf("behavior processing membership err=%v", err)
	}
	if exists, err := client.HExists(likes.BehaviorClaimsKey, pair).Result(); err != nil || exists {
		t.Fatalf("behavior claims membership=%t err=%v", exists, err)
	}
}

func createPostForLikeIntegration(t *testing.T, userID uint, body string) postResponse {
	t.Helper()
	ctx, recorder := newReplyIntegrationContext(http.MethodPost, "/api/posts", "", body, userID)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create post status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == 0 {
		t.Fatalf("create post response=%#v", response)
	}
	return response
}

func createCanonicalReplyForLikeIntegration(t *testing.T, userID, parentID uint, content string) postResponse {
	t.Helper()
	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts",
		strconvUint(parentID),
		fmt.Sprintf(`{"content":%q,"reply_to_post_id":%d}`, content, parentID),
		userID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create canonical reply status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == 0 {
		t.Fatalf("create dedicated reply response=%#v", response)
	}
	return response
}

func assertPostLikeStateIntegration(t *testing.T, postID, userID uint, wantLikes int64, wantLiked bool) {
	t.Helper()
	ctx, recorder := newReplyIntegrationContext(
		http.MethodGet,
		"/api/posts/"+strconvUint(postID)+"/like",
		strconvUint(postID),
		"",
		userID,
	)
	GetPostLikes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get like state post=%d status=%d body=%s", postID, recorder.Code, recorder.Body.String())
	}
	var state postLikeIntegrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Likes != wantLikes || state.Liked != wantLiked {
		t.Fatalf("post=%d state=%#v want likes=%d liked=%t", postID, state, wantLikes, wantLiked)
	}
}

func assertPostLikeMutationIntegration(t *testing.T, postID, userID uint, liked bool, wantLikes int64, wantLiked bool) {
	t.Helper()
	ctx, recorder := newReplyIntegrationContext(
		map[bool]string{true: http.MethodPut, false: http.MethodDelete}[liked],
		"/api/posts/"+strconvUint(postID)+"/like",
		strconvUint(postID),
		"",
		userID,
	)
	if liked {
		LikePost(ctx)
	} else {
		UnlikePost(ctx)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("mutate like post=%d liked=%t status=%d body=%s", postID, liked, recorder.Code, recorder.Body.String())
	}
	var state postLikeIntegrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Likes != wantLikes || state.Liked != wantLiked {
		t.Fatalf("post=%d mutation=%#v want likes=%d liked=%t", postID, state, wantLikes, wantLiked)
	}
}

func assertPostLikeStatesIntegration(t *testing.T, postIDs []uint, userID uint) {
	t.Helper()
	body := fmt.Sprintf(`{"post_ids":[%d,%d,%d]}`, postIDs[0], postIDs[1], postIDs[2])
	ctx, recorder := newReplyIntegrationContext(http.MethodPost, "/api/posts/like-states", "", body, userID)
	GetPostLikeStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk like states status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postLikeStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.UnavailablePostIDs) != 0 {
		t.Fatalf("bulk like states unavailable=%v response=%#v", response.UnavailablePostIDs, response)
	}
	if len(response.Items) != len(postIDs) {
		t.Fatalf("bulk like states items=%#v", response.Items)
	}
	for _, item := range response.Items {
		if item.PostID == postIDs[1] && (item.Likes != 0 || item.Liked) {
			t.Fatalf("dedicated reply bulk state=%#v", item)
		}
	}
}

type postLikeIntegrationResponse struct {
	Likes int64 `json:"likes"`
	Liked bool  `json:"liked"`
}
