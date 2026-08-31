package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type replyIntegrationFixture struct {
	Author    models.User
	Commenter models.User
	Other     models.User
	Article   models.Post
}

func openReplyIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.PostBehavior{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{
		Kafka: config.KafkaConfig{
			ActivityEventsTopic: "goexchange.activity.events.v1",
		},
	}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})
	return db
}

func newReplyIntegrationFixture(t *testing.T, db *gorm.DB) replyIntegrationFixture {
	t.Helper()
	fixture := replyIntegrationFixture{
		Author:    models.User{Username: "comment-author-" + uuid.NewString(), Password: "test"},
		Commenter: models.User{Username: "commenter-" + uuid.NewString(), Password: "test", DisplayName: "Old Name", AvatarURL: "old.jpg"},
		Other:     models.User{Username: "commenter-other-" + uuid.NewString(), Password: "test"},
	}
	if err := db.Create(&fixture.Author).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&fixture.Commenter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&fixture.Other).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fixture.Article = models.Post{
		Model: gorm.Model{CreatedAt: now, UpdatedAt: now}, AuthorID: fixture.Author.ID,
		Content: "comment fixture", Visibility: "public",
	}
	if err := db.Create(&fixture.Article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostArticle{PostID: fixture.Article.ID, Title: "comment fixture", Preview: "comment fixture", PublicationState: consts.PostPublicationStatePublished, PublishedAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("reply_to_post_id = ?", fixture.Article.ID).Delete(&models.Post{})
		db.Unscoped().Where("post_id = ?", fixture.Article.ID).Delete(&models.PostArticle{})
		db.Unscoped().Where("post_id = ? OR user_id IN ?", fixture.Article.ID, []uint{fixture.Commenter.ID, fixture.Other.ID}).Delete(&models.PostBehavior{})
		db.Unscoped().Delete(&fixture.Article)
		db.Unscoped().Where("id IN ?", []uint{fixture.Author.ID, fixture.Commenter.ID, fixture.Other.ID}).Delete(&models.User{})
	})
	return fixture
}

func newReplyIntegrationContext(method, target, id, body string, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	if userID != 0 {
		ctx.Set("user_id", userID)
	}
	return ctx, recorder
}

func createReplyRecord(t *testing.T, db *gorm.DB, postID, userID uint, content string, createdAt time.Time) models.Post {
	t.Helper()
	conversationID := postID
	comment := models.Post{AuthorID: userID, ReplyToPostID: &postID, ConversationID: &conversationID, Content: content, Visibility: "public"}
	if !createdAt.IsZero() {
		comment.Model.CreatedAt = createdAt
		comment.Model.UpdatedAt = createdAt
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Post{}).
			Where("id = ?", postID).
			UpdateColumn("reply_count", gorm.Expr("reply_count + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture post counter update affected an unexpected number of rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return comment
}

func TestCreatePostReplyIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	ctx, recorder := newReplyIntegrationContext(
		http.MethodPost,
		"/api/posts",
		strconvUint(fixture.Article.ID),
		`{"content":"  hello  ","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`,"user_id":999,"author_id":999,"post_id":999}`,
		fixture.Commenter.ID,
	)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response replyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ReplyToPostID == nil || *response.ReplyToPostID != fixture.Article.ID || response.Content != "hello" || response.Author.ID != fixture.Commenter.ID || response.Author.Username != fixture.Commenter.Username || response.Author.DisplayName != "Old Name" || response.Author.AvatarURL != "old.jpg" {
		t.Fatalf("response=%#v", response)
	}
	var comment models.Post
	if err := db.First(&comment, response.ID).Error; err != nil {
		t.Fatal(err)
	}
	if comment.ReplyToPostID == nil || *comment.ReplyToPostID != fixture.Article.ID || comment.AuthorID != fixture.Commenter.ID || comment.Content != "hello" {
		t.Fatalf("stored comment=%#v", comment)
	}

	if replyPostCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after first create=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	ctx, recorder = newReplyIntegrationContext(http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID), `{"content":"second","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`, fixture.Commenter.ID)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("second create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 2 {
		t.Fatalf("count after second create=%d", replyPostCount(t, db, fixture.Article.ID))
	}

	for _, forbidden := range []string{"user_id", "UserID", "password", "Password", "DeletedAt"} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("response leaked %s: %s", forbidden, recorder.Body.String())
		}
	}

	ctx, recorder = newReplyIntegrationContext(http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID), `{"content":"missing user","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`, 0)
	createPost(ctx, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing user status=%d", recorder.Code)
	}

	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Model(&models.PostArticle{}).Where("post_id = ?", fixture.Article.ID).Update("expired_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newReplyIntegrationContext(http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID), `{"content":"expired","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`, fixture.Commenter.ID)
	createPost(ctx, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expired article status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if replyPostCount(t, db, fixture.Article.ID) != 2 {
		t.Fatalf("expired create changed count=%d", replyPostCount(t, db, fixture.Article.ID))
	}
}

func TestGetPostCommentsCursorIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	posts := []models.Post{
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "first", now),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "fourth", now),
	}
	removed := createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "removed", now.Add(-time.Second))
	softDeleteCounterAwareComment(t, db, removed)

	otherArticle := models.Post{AuthorID: fixture.Author.ID, Content: "other post", Visibility: "public"}
	if err := db.Create(&otherArticle).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("reply_to_post_id = ?", otherArticle.ID).Delete(&models.Post{})
		db.Unscoped().Delete(&otherArticle)
	})
	createReplyRecord(t, db, otherArticle.ID, fixture.Commenter.ID, "unrelated", now.Add(time.Second))

	path := "/api/posts/" + strconvUint(fixture.Article.ID) + "/replies?limit=2"
	ctx, recorder := newReplyIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%#v", first)
	}
	if first.Items[0].ID <= first.Items[1].ID {
		t.Fatalf("same timestamp order should use id DESC: %#v", first.Items)
	}

	secondPath := path + "&cursor=" + *first.NextCursor
	ctx, recorder = newReplyIntegrationContext(http.MethodGet, secondPath, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatalf("second page=%#v", second)
	}

	seen := make(map[uint]struct{}, len(first.Items)+len(second.Items))
	for _, item := range append(first.Items, second.Items...) {
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("duplicate item id=%d", item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.ReplyToPostID == nil || *item.ReplyToPostID != fixture.Article.ID || item.ID == removed.ID {
			t.Fatalf("unexpected item=%#v", item)
		}
	}
	for _, comment := range posts {
		if _, exists := seen[comment.ID]; !exists {
			t.Fatalf("missing comment id=%d", comment.ID)
		}
	}
}

func TestDeleteReplyRowsAffectedIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	owned := createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "owned", time.Time{})
	ctx, recorder := newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Other.ID)
	DeletePostReply(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("other user delete status=%d", recorder.Code)
	}
	if err := db.First(&models.Post{}, owned.ID).Error; err != nil {
		t.Fatalf("forbidden delete removed comment: %v", err)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("forbidden delete changed count=%d", replyPostCount(t, db, fixture.Article.ID))
	}

	ctx, recorder = newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Commenter.ID)
	DeletePostReply(ctx)
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("owner delete status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var deleted models.Post
	if err := db.Unscoped().First(&deleted, owned.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft deleted=%#v err=%v", deleted, err)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("owner delete count=%d", replyPostCount(t, db, fixture.Article.ID))
	}
	ctx, recorder = newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Commenter.ID)
	DeletePostReply(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("repeat delete status=%d", recorder.Code)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("repeat delete changed count=%d", replyPostCount(t, db, fixture.Article.ID))
	}

	expiring := createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "expire then delete", time.Time{})
	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Model(&fixture.Article).Update("expired_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(expiring.ID), strconvUint(expiring.ID), "", fixture.Commenter.ID)
	DeletePostReply(ctx)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete after article expiry status=%d", recorder.Code)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("delete after expiry count=%d", replyPostCount(t, db, fixture.Article.ID))
	}

	concurrent := createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "concurrent delete", time.Time{})
	var waitGroup sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			deleteCtx, deleteRecorder := newReplyIntegrationContext(http.MethodDelete, "/api/posts/"+strconvUint(concurrent.ID), strconvUint(concurrent.ID), "", fixture.Commenter.ID)
			DeletePostReply(deleteCtx)
			statuses <- deleteRecorder.Code
		}()
	}
	waitGroup.Wait()
	close(statuses)

	successes, notFound := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			successes++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("concurrent delete status=%d", status)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("concurrent delete successes=%d notFound=%d", successes, notFound)
	}
	if replyPostCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("concurrent delete count=%d", replyPostCount(t, db, fixture.Article.ID))
	}
}
func TestGetPostCommentsEmptyFeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)

	ctx, recorder := newReplyIntegrationContext(
		http.MethodGet,
		"/api/posts/"+strconvUint(fixture.Article.ID)+"/replies?limit=20",
		strconvUint(fixture.Article.ID),
		"",
		fixture.Commenter.ID,
	)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response["items"]) != "[]" {
		t.Fatalf("items=%s, want []", response["items"])
	}
	if string(response["next_cursor"]) != "null" {
		t.Fatalf("next_cursor=%s, want null", response["next_cursor"])
	}
}

func TestReplyCursorStableAfterDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	posts := []models.Post{
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "newest", now),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now.Add(-time.Second)),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now.Add(-2*time.Second)),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "oldest", now.Add(-3*time.Second)),
	}

	path := "/api/posts/" + strconvUint(fixture.Article.ID) + "/replies?limit=2"
	ctx, recorder := newReplyIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%#v", first)
	}
	if first.Items[0].ID != posts[0].ID || first.Items[1].ID != posts[1].ID {
		t.Fatalf("unexpected first page=%#v", first.Items)
	}

	softDeleteCounterAwareComment(t, db, posts[0])
	ctx, recorder = newReplyIntegrationContext(http.MethodGet, path+"&cursor="+*first.NextCursor, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatalf("second page=%#v", second)
	}
	for index, expected := range []models.Post{posts[2], posts[3]} {
		if second.Items[index].ID != expected.ID {
			t.Fatalf("second page item %d id=%d want=%d", index, second.Items[index].ID, expected.ID)
		}
	}
	for _, item := range second.Items {
		for _, alreadyLoaded := range first.Items {
			if item.ID == alreadyLoaded.ID {
				t.Fatalf("duplicate across pages id=%d", item.ID)
			}
		}
	}
}

func TestReplyCursorStableAfterNewComment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	posts := []models.Post{
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "newest", now),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now.Add(-time.Second)),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now.Add(-2*time.Second)),
		createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "oldest", now.Add(-3*time.Second)),
	}

	path := "/api/posts/" + strconvUint(fixture.Article.ID) + "/replies?limit=2"
	ctx, recorder := newReplyIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%#v", first)
	}

	inserted := createReplyRecord(t, db, fixture.Article.ID, fixture.Other.ID, "inserted after first page", now.Add(time.Second))
	ctx, recorder = newReplyIntegrationContext(http.MethodGet, path+"&cursor="+*first.NextCursor, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatalf("second page=%#v", second)
	}
	for index, expected := range []models.Post{posts[2], posts[3]} {
		if second.Items[index].ID != expected.ID {
			t.Fatalf("second page item %d id=%d want=%d", index, second.Items[index].ID, expected.ID)
		}
	}
	for _, item := range append(first.Items, second.Items...) {
		if item.ID == inserted.ID {
			t.Fatalf("newer comment %d leaked into cursor continuation", inserted.ID)
		}
	}
}
func TestReplyAuthorUsesCurrentUserIdentityIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	historical := createReplyRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "historical", time.Now().UTC())

	if err := db.Model(&fixture.Commenter).Updates(map[string]interface{}{
		"display_name": "New Name",
		"avatar_url":   "new.jpg",
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, recorder := newReplyIntegrationContext(http.MethodGet, "/api/posts/"+strconvUint(fixture.Article.ID)+"/replies", strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("historical posts status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var posts replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &posts); err != nil {
		t.Fatal(err)
	}
	var historicalResponse *replyResponse
	for index := range posts.Items {
		if posts.Items[index].ID == historical.ID {
			historicalResponse = &posts.Items[index]
			break
		}
	}
	if historicalResponse == nil || historicalResponse.Author.DisplayName != "New Name" || historicalResponse.Author.AvatarURL != "new.jpg" {
		t.Fatalf("historical comment identity=%#v", historicalResponse)
	}

	ctx, recorder = newReplyIntegrationContext(http.MethodPost, "/api/posts", strconvUint(fixture.Article.ID), `{"content":"new reply","reply_to_post_id":`+strconvUint(fixture.Article.ID)+`}`, fixture.Commenter.ID)
	createPost(ctx, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("new comment status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created replyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Author.DisplayName != "New Name" || created.Author.AvatarURL != "new.jpg" {
		t.Fatalf("new comment identity=%#v", created.Author)
	}
}
