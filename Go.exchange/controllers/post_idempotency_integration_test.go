package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func ensureClientPublishSchemaForIntegration(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		"ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_client_publish_identity",
		"ALTER TABLE posts ADD CONSTRAINT chk_posts_client_publish_identity CHECK ((client_publish_id IS NULL AND client_publish_fingerprint IS NULL) OR (client_publish_id IS NOT NULL AND client_publish_fingerprint IS NOT NULL AND char_length(client_publish_fingerprint) = 64))",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_posts_author_client_publish_id ON posts (author_id, client_publish_id) WHERE client_publish_id IS NOT NULL",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("apply client publish schema: %v", err)
		}
	}
}

func newClientPublishIntegrationContext(body string, userID uint, key uuid.UUID) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Idempotency-Key", key.String())
	ctx.Set("user_id", userID)
	return ctx, recorder
}

func decodeCreatedPost(t *testing.T, recorder *httptest.ResponseRecorder) postResponse {
	t.Helper()
	var response postResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode post response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func trackIntegrationPost(fixture *postEmbeddingOutboxFixture, postID uint) {
	if postID == 0 {
		return
	}
	for _, post := range fixture.posts {
		if post.ID == postID {
			return
		}
	}
	fixture.posts = append(fixture.posts, models.Post{Model: gorm.Model{ID: postID}})
}

func TestCreatePostIdempotencyReplayConflictScopeAndDeleteIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)

	key := uuid.New()
	body := `{"content":"  idempotent root  "}`
	ctx, recorder := newClientPublishIntegrationContext(body, fixture.users[0].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, first.ID)
	if first.Content != "idempotent root" || first.Author.ID != fixture.users[0].ID || first.Media == nil {
		t.Fatalf("first response=%#v", first)
	}

	var stored models.Post
	if err := db.Unscoped().First(&stored, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ClientPublishID == nil || *stored.ClientPublishID != key || stored.ClientPublishFingerprint == nil || len(*stored.ClientPublishFingerprint) != 64 {
		t.Fatalf("stored publish identity=%#v", stored)
	}

	ctx, recorder = newClientPublishIntegrationContext(body, fixture.users[0].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	replay := decodeCreatedPost(t, recorder)
	if replay.ID != first.ID || replay.Author.ID != fixture.users[0].ID || replay.Content != first.Content || replay.Media == nil {
		t.Fatalf("replay response=%#v first=%#v", replay, first)
	}
	var postCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("author_id = ? AND client_publish_id = ?", fixture.users[0].ID, key).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 1 {
		t.Fatalf("same-key post count=%d want=1", postCount)
	}

	ctx, recorder = newClientPublishIntegrationContext(`{"content":"different payload"}`, fixture.users[0].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"POST_IDEMPOTENCY_CONFLICT"`) {
		t.Fatalf("conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = newClientPublishIntegrationContext(body, fixture.users[1].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("scoped create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	other := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, other.ID)
	if other.ID == first.ID || other.Author.ID != fixture.users[1].ID {
		t.Fatalf("scoped response=%#v first=%#v", other, first)
	}
	var scopedCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("client_publish_id = ?", key).Count(&scopedCount).Error; err != nil {
		t.Fatal(err)
	}
	if scopedCount != 2 {
		t.Fatalf("same key across users count=%d want=2", scopedCount)
	}

	deletedKey := uuid.New()
	ctx, recorder = newClientPublishIntegrationContext(`{"content":"delete once"}`, fixture.users[0].ID, deletedKey)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("deleted fixture create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	deleted := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, deleted.ID)
	if err := db.Delete(&models.Post{}, deleted.ID).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newClientPublishIntegrationContext(`{"content":"delete once"}`, fixture.users[0].ID, deletedKey)
	createPost(ctx)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"POST_IDEMPOTENCY_CONFLICT"`) {
		t.Fatalf("deleted replay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostIdempotencyMediaReplayDoesNotDuplicateRowsIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)

	originalStat := statStoredObject
	originalRead := readStoredObject
	t.Cleanup(func() {
		statStoredObject = originalStat
		readStoredObject = originalRead
	})
	statStoredObject = func(context.Context, string) error { return nil }
	mediaID := uuid.NewString()
	readStoredObject = func(_ context.Context, _ string, _ int64) ([]byte, error) {
		return testPostMediaManifestJSONFor(fixture.users[0].ID, mediaID), nil
	}
	mediaURL := "/api/files/post-media/users/v1/" + strconv.FormatUint(uint64(fixture.users[0].ID), 10) + "/" + mediaID + "/medium.jpg"
	body := `{"content":"caption","media":[{"type":"image","url":"` + mediaURL + `"}]}`
	key := uuid.New()
	ctx, recorder := newClientPublishIntegrationContext(body, fixture.users[0].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("first media status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, first.ID)
	if len(first.Media) != 1 {
		t.Fatalf("first media=%#v", first.Media)
	}

	ctx, recorder = newClientPublishIntegrationContext(body, fixture.users[0].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("media replay status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	replay := decodeCreatedPost(t, recorder)
	if replay.ID != first.ID || len(replay.Media) != 1 || replay.Media[0].URL != mediaURL {
		t.Fatalf("media replay=%#v first=%#v", replay, first)
	}
	var mediaCount int64
	if err := db.Model(&models.PostMedia{}).Where("post_id = ?", first.ID).Count(&mediaCount).Error; err != nil {
		t.Fatal(err)
	}
	if mediaCount != 1 {
		t.Fatalf("post media rows=%d want=1", mediaCount)
	}
}

func TestCreatePostIdempotencyReplyReplayDoesNotDuplicateSideEffectsIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	parent := models.Post{
		Model:    gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID: fixture.users[0].ID, Content: "reply parent", Language: "und", Visibility: "public",
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	trackIntegrationPost(fixture, parent.ID)

	key := uuid.New()
	body := `{"content":"reply once","reply_to_post_id":` + strconv.FormatUint(uint64(parent.ID), 10) + `}`
	ctx, recorder := newClientPublishIntegrationContext(body, fixture.users[1].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("first reply status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	first := decodeCreatedPost(t, recorder)
	trackIntegrationPost(fixture, first.ID)

	ctx, recorder = newClientPublishIntegrationContext(body, fixture.users[1].ID, key)
	createPost(ctx)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("reply replay status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	replay := decodeCreatedPost(t, recorder)
	if replay.ID != first.ID || replay.ReplyToPostID == nil || *replay.ReplyToPostID != parent.ID {
		t.Fatalf("reply replay=%#v first=%#v", replay, first)
	}
	var storedParent models.Post
	if err := db.First(&storedParent, parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedParent.ReplyCount != 1 {
		t.Fatalf("parent reply count=%d want=1", storedParent.ReplyCount)
	}
	var behavior models.PostBehavior
	if err := db.Where("user_id = ? AND post_id = ? AND action = ?", fixture.users[1].ID, parent.ID, PostBehaviorActionReply).First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != 1 {
		t.Fatalf("reply behavior count=%d want=1", behavior.Count)
	}
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).Where("aggregate_id = ?", strconv.FormatUint(uint64(first.ID), 10)).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 2 {
		t.Fatalf("reply outbox rows=%d want=2", outboxCount)
	}
	var replyCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("author_id = ? AND client_publish_id = ?", fixture.users[1].ID, key).Count(&replyCount).Error; err != nil {
		t.Fatal(err)
	}
	if replyCount != 1 {
		t.Fatalf("reply post rows=%d want=1", replyCount)
	}
}

type idempotencyIntegrationResult struct {
	status int
	replay string
	postID uint
	body   string
}

func runClientPublishIntegrationRequest(body string, userID uint, key uuid.UUID) idempotencyIntegrationResult {
	ctx, recorder := newClientPublishIntegrationContext(body, userID, key)
	createPost(ctx)
	result := idempotencyIntegrationResult{status: recorder.Code, replay: recorder.Header().Get("Idempotency-Replayed"), body: recorder.Body.String()}
	if recorder.Code == http.StatusCreated || recorder.Code == http.StatusOK {
		var response postResponse
		if json.Unmarshal(recorder.Body.Bytes(), &response) == nil {
			result.postID = response.ID
		}
	}
	return result
}

func TestCreatePostIdempotencyConcurrentSameKeyCreatesOnePostIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	key := uuid.New()
	body := `{"content":"concurrent root"}`
	results := make(chan idempotencyIntegrationResult, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			results <- runClientPublishIntegrationRequest(body, fixture.users[0].ID, key)
		}()
	}
	waitGroup.Wait()
	close(results)

	collected := make([]idempotencyIntegrationResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
		trackIntegrationPost(fixture, result.postID)
	}
	if len(collected) != 2 {
		t.Fatalf("results=%#v", collected)
	}
	statuses := []int{collected[0].status, collected[1].status}
	sort.Ints(statuses)
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusCreated {
		t.Fatalf("concurrent statuses=%v results=%#v", statuses, collected)
	}
	if collected[0].postID == 0 || collected[0].postID != collected[1].postID {
		t.Fatalf("concurrent post IDs=%#v", collected)
	}
	var postCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("author_id = ? AND client_publish_id = ?", fixture.users[0].ID, key).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 1 {
		t.Fatalf("concurrent post rows=%d want=1", postCount)
	}
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).Where("aggregate_id = ?", strconv.FormatUint(uint64(collected[0].postID), 10)).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("concurrent outbox rows=%d want=1", outboxCount)
	}
}

func TestCreatePostIdempotencyConcurrentDifferentPayloadConflictsIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	ensureClientPublishSchemaForIntegration(t, db)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	key := uuid.New()
	bodies := []string{`{"content":"concurrent A"}`, `{"content":"concurrent B"}`}
	results := make(chan idempotencyIntegrationResult, len(bodies))
	var waitGroup sync.WaitGroup
	for _, body := range bodies {
		waitGroup.Add(1)
		go func(body string) {
			defer waitGroup.Done()
			results <- runClientPublishIntegrationRequest(body, fixture.users[0].ID, key)
		}(body)
	}
	waitGroup.Wait()
	close(results)

	created, conflicts := 0, 0
	var createdID uint
	for result := range results {
		trackIntegrationPost(fixture, result.postID)
		switch result.status {
		case http.StatusCreated:
			created++
			createdID = result.postID
		case http.StatusConflict:
			conflicts++
			if !strings.Contains(result.body, `"code":"POST_IDEMPOTENCY_CONFLICT"`) {
				t.Fatalf("conflict body=%s", result.body)
			}
		default:
			t.Fatalf("unexpected concurrent conflict result=%#v", result)
		}
	}
	if created != 1 || conflicts != 1 || createdID == 0 {
		t.Fatalf("created=%d conflicts=%d id=%d", created, conflicts, createdID)
	}
	var postCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("author_id = ? AND client_publish_id = ?", fixture.users[0].ID, key).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 1 {
		t.Fatalf("different-payload post rows=%d want=1", postCount)
	}
}
