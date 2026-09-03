package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"Go.exchange/likes"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

func TestCanonicalPostDeletePGRedisIdentityE2E(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	if os.Getenv("REDIS_TEST_ADDR") == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
	}

	db := openReplyIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.PostReaction{}, &models.PostRepost{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	redisClient := openPostLikeIntegrationRedis(t)

	alice := models.User{Username: "post-e2e-alice-" + uuid.NewString(), Password: "test"}
	bob := models.User{Username: "post-e2e-bob-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&alice, &bob}).Error; err != nil {
		t.Fatal(err)
	}
	createdIDs := make([]uint, 0, 4)
	userIDs := []uint{alice.ID, bob.ID}
	t.Cleanup(func() {
		if err := cleanupPostLikeIntegrationState(redisClient, createdIDs, userIDs); err != nil {
			t.Errorf("cleanup post-like Redis integration state: %v", err)
		}
		if len(createdIDs) > 0 {
			stringIDs := make([]string, 0, len(createdIDs))
			for _, postID := range createdIDs {
				stringIDs = append(stringIDs, strconvUint(postID))
			}
			db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostReaction{})
			db.Unscoped().Where("post_id IN ?", createdIDs).Delete(&models.PostRepost{})
			db.Unscoped().Where("post_id IN ? OR user_id IN ?", createdIDs, userIDs).Delete(&models.PostBehavior{})
			db.Unscoped().Where("aggregate_id IN ?", stringIDs).Delete(&models.OutboxEvent{})
			db.Unscoped().Where("id IN ?", createdIDs).Delete(&models.Post{})
		}
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	p1Content := "P1 canonical root secret"
	p1 := createPostForLikeIntegration(t, alice.ID, fmt.Sprintf(`{"content":%q}`, p1Content))
	createdIDs = append(createdIDs, p1.ID)
	p2 := createPostForLikeIntegration(t, bob.ID, fmt.Sprintf(`{"content":%q,"reply_to_post_id":%d}`, "P2 reply", p1.ID))
	createdIDs = append(createdIDs, p2.ID)
	p3 := createPostForLikeIntegration(t, alice.ID, fmt.Sprintf(`{"content":%q,"reply_to_post_id":%d}`, "P3 nested reply", p2.ID))
	createdIDs = append(createdIDs, p3.ID)

	if p2.ReplyToPostID == nil || *p2.ReplyToPostID != p1.ID || p2.ConversationID != p1.ID {
		t.Fatalf("P2 graph=%#v", p2)
	}
	if p3.ReplyToPostID == nil || *p3.ReplyToPostID != p2.ID || p3.ConversationID != p1.ID {
		t.Fatalf("P3 graph=%#v", p3)
	}
	assertPostLikeMutationIntegration(t, p2.ID, bob.ID, true, 1, true)
	assertAuthoritativeLikeStateForIdentityE2E(t, redisClient, p2.ID, bob.ID)
	if state, err := mutatePostRepostForIdentityE2E(t, bob.ID, p1.ID, true); err != nil || !state.Reposted || state.Reposts != 1 {
		t.Fatalf("first repost state=%#v err=%v", state, err)
	}
	if state, err := mutatePostRepostForIdentityE2E(t, bob.ID, p1.ID, true); err != nil || !state.Reposted || state.Reposts != 1 {
		t.Fatalf("duplicate repost state=%#v err=%v", state, err)
	}
	if state, err := mutatePostRepostForIdentityE2E(t, bob.ID, p1.ID, false); err != nil || state.Reposted || state.Reposts != 0 {
		t.Fatalf("undo repost state=%#v err=%v", state, err)
	}

	p4 := createPostForLikeIntegration(t, bob.ID, fmt.Sprintf(`{"content":%q,"quote_post_id":%d}`, "P4 quote", p1.ID))
	createdIDs = append(createdIDs, p4.ID)
	var p4Row models.Post
	if err := db.First(&p4Row, p4.ID).Error; err != nil {
		t.Fatal(err)
	}
	if p4Row.ReplyToPostID != nil || p4Row.QuotePostID == nil || *p4Row.QuotePostID != p1.ID ||
		p4Row.ConversationID != nil || p4Row.DeletedAt.Valid {
		t.Fatalf("P4 stored graph=%#v", p4Row)
	}
	if p4.ConversationID != p4.ID || p4.ReplyToPostID != nil || p4.QuotePostID == nil || *p4.QuotePostID != p1.ID ||
		p4.QuotePost == nil || p4.QuotePost.Deleted || p4.QuotePost.Content != p1Content {
		t.Fatalf("P4 API graph=%#v", p4)
	}
	primedP4, err := loadPostDetail(strconvUint(p4.ID))
	if err != nil {
		t.Fatal(err)
	}
	if primedP4.QuotePost == nil || primedP4.QuotePost.Deleted || primedP4.QuotePost.Content != p1Content {
		t.Fatalf("primed P4 quote=%#v", primedP4.QuotePost)
	}

	if err := db.Create(&models.PostReaction{
		UserID: bob.ID, PostID: p1.ID, Reaction: models.PostReactionLike, Liked: true,
		Version: 1, UpdatedAt: time.Now().UTC(), StateChangedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleteCtx, deleteRecorder := newPostDeleteContext(strconvUint(p1.ID), &alice.ID)
	DeletePost(deleteCtx)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("P1 delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	if status, _ := getCanonicalPostJSONForIdentityE2E(t, p1.ID); status != http.StatusNotFound {
		t.Fatalf("normal P1 GET status=%d want 404", status)
	}
	p2Status, p2Payload := getCanonicalPostJSONForIdentityE2E(t, p2.ID)
	if p2Status != http.StatusOK {
		t.Fatalf("P2 GET status=%d payload=%v", p2Status, p2Payload)
	}
	p3Status, p3Payload := getCanonicalPostJSONForIdentityE2E(t, p3.ID)
	if p3Status != http.StatusOK {
		t.Fatalf("P3 GET status=%d payload=%v", p3Status, p3Payload)
	}
	p4Status, p4Payload := getCanonicalPostJSONForIdentityE2E(t, p4.ID)
	if p4Status != http.StatusOK {
		t.Fatalf("P4 GET status=%d payload=%v", p4Status, p4Payload)
	}
	assertCanonicalActivePostPayload(t, p2Payload, "P2 reply")
	assertCanonicalActivePostPayload(t, p3Payload, "P3 nested reply")
	assertCanonicalActivePostPayload(t, p4Payload, "P4 quote")
	assertCanonicalTombstonePayload(t, p2Payload["reply_to_post"], p1.ID)
	assertCanonicalTombstonePayload(t, p4Payload["quote_post"], p1.ID)
	for _, payload := range []map[string]json.RawMessage{p2Payload, p4Payload} {
		if bytesContainRawPayload(payload, p1Content) || bytesContainRawPayload(payload, alice.Username) {
			t.Fatalf("deleted P1 data leaked in payload=%v", payload)
		}
	}

	cachedP4, err := loadPostDetail(strconvUint(p4.ID))
	if err != nil {
		t.Fatal(err)
	}
	if cachedP4.QuotePost == nil || !cachedP4.QuotePost.Deleted || cachedP4.QuotePost.ID != p1.ID || cachedP4.QuotePost.Author != nil || cachedP4.QuotePost.Content != "" {
		t.Fatalf("cached P4 quote after P1 delete=%#v", cachedP4.QuotePost)
	}

	var graph []models.Post
	if err := db.Unscoped().Where("id IN ?", createdIDs).Find(&graph).Error; err != nil {
		t.Fatal(err)
	}
	postsByID := make(map[uint]models.Post, len(graph))
	for _, post := range graph {
		postsByID[post.ID] = post
	}
	if !postsByID[p1.ID].DeletedAt.Valid || postsByID[p2.ID].DeletedAt.Valid || postsByID[p3.ID].DeletedAt.Valid || postsByID[p4.ID].DeletedAt.Valid {
		t.Fatalf("post deletion graph=%#v", postsByID)
	}
	if postsByID[p1.ID].ReplyCount != 1 || postsByID[p2.ID].ReplyCount != 1 {
		t.Fatalf("reply counts P1=%d P2=%d", postsByID[p1.ID].ReplyCount, postsByID[p2.ID].ReplyCount)
	}
	var repostCount int64
	if err := db.Model(&models.PostRepost{}).Where("user_id = ? AND post_id = ?", bob.ID, p1.ID).Count(&repostCount).Error; err != nil {
		t.Fatal(err)
	}
	if repostCount != 0 {
		t.Fatalf("active B->P1 reposts=%d", repostCount)
	}
	var behavior models.PostBehavior
	if err := db.Where("user_id = ? AND post_id = ? AND action = ?", bob.ID, p1.ID, PostBehaviorActionReply).First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 1 || !behavior.Active {
		t.Fatalf("B reply behavior=%#v", behavior)
	}
	var behaviorOnDirectChild int64
	if err := db.Model(&models.PostBehavior{}).Where("user_id = ? AND post_id = ? AND action = ?", bob.ID, p2.ID, PostBehaviorActionReply).Count(&behaviorOnDirectChild).Error; err != nil {
		t.Fatal(err)
	}
	if behaviorOnDirectChild != 0 {
		t.Fatalf("B behavior incorrectly keyed to P2=%d", behaviorOnDirectChild)
	}
	assertPostLikeStateIntegration(t, p2.ID, bob.ID, 1, true)
	assertPurgedPostLikeStateForIdentityE2E(t, redisClient, p1.ID)
	if exists, err := redisClient.HExists(likes.BehaviorStateKey, likes.BehaviorPair(bob.ID, p2.ID)).Result(); err != nil || !exists {
		t.Fatalf("P2 behavior relay exists=%t err=%v", exists, err)
	}
	var reactionCount int64
	if err := db.Model(&models.PostReaction{}).Where("user_id = ? AND post_id = ?", bob.ID, p1.ID).Count(&reactionCount).Error; err != nil {
		t.Fatal(err)
	}
	if reactionCount != 1 {
		t.Fatalf("relational P1 reaction count=%d want 1", reactionCount)
	}
}

func mutatePostRepostForIdentityE2E(t *testing.T, userID, postID uint, reposted bool) (postRepostStateResult, error) {
	t.Helper()
	method := http.MethodDelete
	if reposted {
		method = http.MethodPut
	}
	ctx, recorder := newReplyIntegrationContext(method, "/api/posts/"+strconvUint(postID)+"/repost", strconvUint(postID), "", userID)
	if reposted {
		RepostPost(ctx)
	} else {
		UndoRepostPost(ctx)
	}
	if recorder.Code != http.StatusOK {
		return postRepostStateResult{}, fmt.Errorf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response postRepostStateResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		return postRepostStateResult{}, err
	}
	return response, nil
}

func getCanonicalPostJSONForIdentityE2E(t *testing.T, postID uint) (int, map[string]json.RawMessage) {
	t.Helper()
	ctx, recorder := newReplyIntegrationContext(http.MethodGet, "/api/posts/"+strconvUint(postID), strconvUint(postID), "", 0)
	GetPostByID(ctx)
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return recorder.Code, payload
}

func assertCanonicalActivePostPayload(t *testing.T, payload map[string]json.RawMessage, content string) {
	t.Helper()
	if payload == nil || string(payload["content"]) != fmt.Sprintf("%q", content) || string(payload["deleted"]) != "false" {
		t.Fatalf("active post payload=%v", payload)
	}
}

func assertCanonicalTombstonePayload(t *testing.T, raw json.RawMessage, postID uint) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 {
		t.Fatalf("tombstone payload=%v", payload)
	}
	var gotID uint
	var deleted bool
	if err := json.Unmarshal(payload["id"], &gotID); err != nil || gotID != postID {
		t.Fatalf("tombstone id=%d err=%v want=%d", gotID, err, postID)
	}
	if err := json.Unmarshal(payload["deleted"], &deleted); err != nil || !deleted {
		t.Fatalf("tombstone deleted=%t err=%v", deleted, err)
	}
	for _, key := range []string{"state", "author", "content", "published_at"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("tombstone leaked %q: %v", key, payload)
		}
	}
}

func bytesContainRawPayload(payload map[string]json.RawMessage, needle string) bool {
	encoded, _ := json.Marshal(payload)
	return len(needle) > 0 && containsBytes(encoded, []byte(needle))
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for index := 0; index+len(needle) <= len(haystack); index++ {
		match := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func assertPurgedPostLikeStateForIdentityE2E(t *testing.T, client *redis.Client, postID uint) {
	t.Helper()
	for _, key := range []string{likes.ReadyKey(postID), likes.CountKey(postID), likes.UsersKey(postID), likes.VersionKey(postID)} {
		if exists, err := client.Exists(key).Result(); err != nil || exists != 0 {
			t.Fatalf("purged key=%q exists=%d err=%v", key, exists, err)
		}
	}
	if registered, err := client.SIsMember(likes.RegistryKey, postID).Result(); err != nil || registered {
		t.Fatalf("purged registry=%t err=%v", registered, err)
	}
	if _, err := client.ZScore(likes.ExpiryCandidatesKey, strconvUint(postID)).Result(); err != redis.Nil {
		t.Fatalf("purged expiry candidate err=%v", err)
	}
	if marker, err := client.HExists(likes.RecoverableVersionsKey, strconvUint(postID)).Result(); err != nil || marker {
		t.Fatalf("purged recoverable marker=%t err=%v", marker, err)
	}
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || dirty {
		t.Fatalf("purged dirty=%t err=%v", dirty, err)
	}
	if _, err := client.ZScore(likes.ProcessingKey, strconvUint(postID)).Result(); err != redis.Nil {
		t.Fatalf("purged processing err=%v", err)
	}
	if claims, err := client.HExists(likes.ClaimsKey, strconvUint(postID)).Result(); err != nil || claims {
		t.Fatalf("purged claims=%t err=%v", claims, err)
	}
}

func assertAuthoritativeLikeStateForIdentityE2E(t *testing.T, client *redis.Client, postID, userID uint) {
	t.Helper()
	if ready, err := client.Get(likes.ReadyKey(postID)).Result(); err != nil || ready != "1" {
		t.Fatalf("P2 like ready=%q err=%v", ready, err)
	}
	if count, err := client.Get(likes.CountKey(postID)).Result(); err != nil || count != "1" {
		t.Fatalf("P2 like count=%q err=%v", count, err)
	}
	if version, err := client.Get(likes.VersionKey(postID)).Result(); err != nil || version != "1" {
		t.Fatalf("P2 like version=%q err=%v", version, err)
	}
	if liked, err := client.SIsMember(likes.UsersKey(postID), strconvUint(userID)).Result(); err != nil || !liked {
		t.Fatalf("P2 like user membership=%t err=%v", liked, err)
	}
	if dirty, err := client.SIsMember(likes.DirtyKey, postID).Result(); err != nil || !dirty {
		t.Fatalf("P2 snapshot dirty=%t err=%v", dirty, err)
	}
	pair := likes.BehaviorPair(userID, postID)
	if state, err := client.HGet(likes.BehaviorStateKey, pair).Result(); err != nil || state == "" {
		t.Fatalf("P2 behavior state=%q err=%v", state, err)
	}
	if dirty, err := client.SIsMember(likes.BehaviorDirtyKey, pair).Result(); err != nil || !dirty {
		t.Fatalf("P2 behavior dirty=%t err=%v", dirty, err)
	}
}
