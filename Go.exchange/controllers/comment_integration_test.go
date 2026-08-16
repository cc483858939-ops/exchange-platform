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

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type commentIntegrationFixture struct {
	Author    models.User
	Commenter models.User
	Other     models.User
	Article   models.Article
}

func openCommentIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.Comment{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })
	return db
}

func newCommentIntegrationFixture(t *testing.T, db *gorm.DB) commentIntegrationFixture {
	t.Helper()
	fixture := commentIntegrationFixture{
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
	fixture.Article = models.Article{
		AuthorID:         fixture.Author.ID,
		Title:            "comment fixture",
		Preview:          "comment fixture",
		PublicationState: consts.ArticlePublicationStatePublished,
		PublishedAt:      &now,
	}
	if err := db.Create(&fixture.Article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id = ?", fixture.Article.ID).Delete(&models.Comment{})
		db.Unscoped().Delete(&fixture.Article)
		db.Unscoped().Where("id IN ?", []uint{fixture.Author.ID, fixture.Commenter.ID, fixture.Other.ID}).Delete(&models.User{})
	})
	return fixture
}

func newCommentIntegrationContext(method, target, id, body string, userID uint) (*gin.Context, *httptest.ResponseRecorder) {
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

func createCommentRecord(t *testing.T, db *gorm.DB, articleID, userID uint, content string, createdAt time.Time) models.Comment {
	t.Helper()
	comment := models.Comment{ArticleID: articleID, UserID: userID, Content: content}
	if !createdAt.IsZero() {
		comment.CreatedAt = createdAt
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		result := tx.Model(&models.Article{}).
			Where("id = ?", articleID).
			UpdateColumn("comment_count", gorm.Expr("comment_count + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("fixture article counter update affected an unexpected number of rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return comment
}

func TestCreateArticleCommentIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	ctx, recorder := newCommentIntegrationContext(
		http.MethodPost,
		"/api/articles/"+strconvUint(fixture.Article.ID)+"/comments",
		strconvUint(fixture.Article.ID),
		`{"content":"  hello  ","user_id":999,"author_id":999,"article_id":999}`,
		fixture.Commenter.ID,
	)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var response commentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ArticleID != fixture.Article.ID || response.Content != "hello" || response.Author.ID != fixture.Commenter.ID || response.Author.Username != fixture.Commenter.Username || response.Author.DisplayName != "Old Name" || response.Author.AvatarURL != "old.jpg" {
		t.Fatalf("response=%#v", response)
	}
	var comment models.Comment
	if err := db.First(&comment, response.ID).Error; err != nil {
		t.Fatal(err)
	}
	if comment.ArticleID != fixture.Article.ID || comment.UserID != fixture.Commenter.ID || comment.Content != "hello" {
		t.Fatalf("stored comment=%#v", comment)
	}

	if commentArticleCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("count after first create=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
	ctx, recorder = newCommentIntegrationContext(http.MethodPost, "/api/articles/"+strconvUint(fixture.Article.ID)+"/comments", strconvUint(fixture.Article.ID), `{"content":"second"}`, fixture.Commenter.ID)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("second create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 2 {
		t.Fatalf("count after second create=%d", commentArticleCount(t, db, fixture.Article.ID))
	}

	for _, forbidden := range []string{"user_id", "UserID", "password", "Password", "DeletedAt"} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("response leaked %s: %s", forbidden, recorder.Body.String())
		}
	}

	ctx, recorder = newCommentIntegrationContext(http.MethodPost, "/api/articles/"+strconvUint(fixture.Article.ID)+"/comments", strconvUint(fixture.Article.ID), `{"content":"missing user"}`, 0)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing user status=%d", recorder.Code)
	}

	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Model(&fixture.Article).Update("expired_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newCommentIntegrationContext(http.MethodPost, "/api/articles/"+strconvUint(fixture.Article.ID)+"/comments", strconvUint(fixture.Article.ID), `{"content":"expired"}`, fixture.Commenter.ID)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expired article status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 2 {
		t.Fatalf("expired create changed count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
}

func TestGetArticleCommentsCursorIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	comments := []models.Comment{
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "first", now),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "fourth", now),
	}
	removed := createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "removed", now.Add(-time.Second))
	softDeleteCounterAwareComment(t, db, removed)

	otherArticle := models.Article{AuthorID: fixture.Author.ID, Title: "other comment article", Preview: "other"}
	if err := db.Create(&otherArticle).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id = ?", otherArticle.ID).Delete(&models.Comment{})
		db.Unscoped().Delete(&otherArticle)
	})
	createCommentRecord(t, db, otherArticle.ID, fixture.Commenter.ID, "unrelated", now.Add(time.Second))

	path := "/api/articles/" + strconvUint(fixture.Article.ID) + "/comments?limit=2"
	ctx, recorder := newCommentIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first commentListResponse
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
	ctx, recorder = newCommentIntegrationContext(http.MethodGet, secondPath, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second commentListResponse
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
		if item.ArticleID != fixture.Article.ID || item.ID == removed.ID {
			t.Fatalf("unexpected item=%#v", item)
		}
	}
	for _, comment := range comments {
		if _, exists := seen[comment.ID]; !exists {
			t.Fatalf("missing comment id=%d", comment.ID)
		}
	}
}

func TestDeleteCommentRowsAffectedIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	owned := createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "owned", time.Time{})
	ctx, recorder := newCommentIntegrationContext(http.MethodDelete, "/api/comments/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Other.ID)
	DeleteComment(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("other user delete status=%d", recorder.Code)
	}
	if err := db.First(&models.Comment{}, owned.ID).Error; err != nil {
		t.Fatalf("forbidden delete removed comment: %v", err)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 1 {
		t.Fatalf("forbidden delete changed count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}

	ctx, recorder = newCommentIntegrationContext(http.MethodDelete, "/api/comments/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Commenter.ID)
	DeleteComment(ctx)
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("owner delete status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var deleted models.Comment
	if err := db.Unscoped().First(&deleted, owned.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft deleted=%#v err=%v", deleted, err)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("owner delete count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
	ctx, recorder = newCommentIntegrationContext(http.MethodDelete, "/api/comments/"+strconvUint(owned.ID), strconvUint(owned.ID), "", fixture.Commenter.ID)
	DeleteComment(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("repeat delete status=%d", recorder.Code)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("repeat delete changed count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}

	expiring := createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "expire then delete", time.Time{})
	expiredAt := time.Now().Add(-time.Hour)
	if err := db.Model(&fixture.Article).Update("expired_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newCommentIntegrationContext(http.MethodDelete, "/api/comments/"+strconvUint(expiring.ID), strconvUint(expiring.ID), "", fixture.Commenter.ID)
	DeleteComment(ctx)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete after article expiry status=%d", recorder.Code)
	}
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("delete after expiry count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}

	concurrent := createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "concurrent delete", time.Time{})
	var waitGroup sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			deleteCtx, deleteRecorder := newCommentIntegrationContext(http.MethodDelete, "/api/comments/"+strconvUint(concurrent.ID), strconvUint(concurrent.ID), "", fixture.Commenter.ID)
			DeleteComment(deleteCtx)
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
	if commentArticleCount(t, db, fixture.Article.ID) != 0 {
		t.Fatalf("concurrent delete count=%d", commentArticleCount(t, db, fixture.Article.ID))
	}
}
func TestGetArticleCommentsEmptyFeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)

	ctx, recorder := newCommentIntegrationContext(
		http.MethodGet,
		"/api/articles/"+strconvUint(fixture.Article.ID)+"/comments?limit=20",
		strconvUint(fixture.Article.ID),
		"",
		fixture.Commenter.ID,
	)
	GetArticleComments(ctx)
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

func TestCommentCursorStableAfterDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	comments := []models.Comment{
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "newest", now),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now.Add(-time.Second)),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now.Add(-2*time.Second)),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "oldest", now.Add(-3*time.Second)),
	}

	path := "/api/articles/" + strconvUint(fixture.Article.ID) + "/comments?limit=2"
	ctx, recorder := newCommentIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first commentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%#v", first)
	}
	if first.Items[0].ID != comments[0].ID || first.Items[1].ID != comments[1].ID {
		t.Fatalf("unexpected first page=%#v", first.Items)
	}

	softDeleteCounterAwareComment(t, db, comments[0])
	ctx, recorder = newCommentIntegrationContext(http.MethodGet, path+"&cursor="+*first.NextCursor, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second commentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatalf("second page=%#v", second)
	}
	for index, expected := range []models.Comment{comments[2], comments[3]} {
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

func TestCommentCursorStableAfterNewComment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	comments := []models.Comment{
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "newest", now),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "second", now.Add(-time.Second)),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "third", now.Add(-2*time.Second)),
		createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "oldest", now.Add(-3*time.Second)),
	}

	path := "/api/articles/" + strconvUint(fixture.Article.ID) + "/comments?limit=2"
	ctx, recorder := newCommentIntegrationContext(http.MethodGet, path, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first commentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%#v", first)
	}

	inserted := createCommentRecord(t, db, fixture.Article.ID, fixture.Other.ID, "inserted after first page", now.Add(time.Second))
	ctx, recorder = newCommentIntegrationContext(http.MethodGet, path+"&cursor="+*first.NextCursor, strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second commentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatalf("second page=%#v", second)
	}
	for index, expected := range []models.Comment{comments[2], comments[3]} {
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
func TestCommentAuthorUsesCurrentUserIdentityIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openCommentIntegrationDatabase(t)
	fixture := newCommentIntegrationFixture(t, db)
	historical := createCommentRecord(t, db, fixture.Article.ID, fixture.Commenter.ID, "historical", time.Now().UTC())

	if err := db.Model(&fixture.Commenter).Updates(map[string]interface{}{
		"display_name": "New Name",
		"avatar_url":   "new.jpg",
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, recorder := newCommentIntegrationContext(http.MethodGet, "/api/articles/"+strconvUint(fixture.Article.ID)+"/comments", strconvUint(fixture.Article.ID), "", fixture.Commenter.ID)
	GetArticleComments(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("historical comments status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var comments commentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &comments); err != nil {
		t.Fatal(err)
	}
	var historicalResponse *commentResponse
	for index := range comments.Items {
		if comments.Items[index].ID == historical.ID {
			historicalResponse = &comments.Items[index]
			break
		}
	}
	if historicalResponse == nil || historicalResponse.Author.DisplayName != "New Name" || historicalResponse.Author.AvatarURL != "new.jpg" {
		t.Fatalf("historical comment identity=%#v", historicalResponse)
	}

	ctx, recorder = newCommentIntegrationContext(http.MethodPost, "/api/articles/"+strconvUint(fixture.Article.ID)+"/comments", strconvUint(fixture.Article.ID), `{"content":"new comment"}`, fixture.Commenter.ID)
	CreateArticleComment(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("new comment status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created commentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Author.DisplayName != "New Name" || created.Author.AvatarURL != "new.jpg" {
		t.Fatalf("new comment identity=%#v", created.Author)
	}
}
