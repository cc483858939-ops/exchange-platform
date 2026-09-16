package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openPostBookmarkIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func requestBookmarkMutation(t *testing.T, method string, postID, viewerID uint) (postBookmarkMutationResult, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/api/posts/"+strconv.FormatUint(uint64(postID), 10)+"/bookmark", nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(postID), 10)}}
	ctx.Set("user_id", viewerID)
	if method == http.MethodPut {
		BookmarkPost(ctx)
	} else {
		UnbookmarkPost(ctx)
	}
	var response postBookmarkMutationResult
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder.Code, recorder.Body.String()
}

func requestBookmarkStates(t *testing.T, viewerID uint, postIDs []uint) (postBookmarkStatesResponse, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body, err := json.Marshal(postBookmarkStatesRequest{PostIDs: postIDs})
	if err != nil {
		t.Fatal(err)
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts/bookmark-states", bytesReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("user_id", viewerID)
	GetPostBookmarkStates(ctx)
	var response postBookmarkStatesResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder.Code, recorder.Body.String()
}

func requestBookmarkHistory(t *testing.T, viewerID uint, query string) (postPageResponse, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/api/me/bookmarks"
	if query != "" {
		path += "?" + query
	}
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("user_id", viewerID)
	GetMyBookmarks(ctx)
	var response postPageResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder.Code, recorder.Body.String()
}

func bytesReader(body []byte) *bytes.Buffer {
	return bytes.NewBuffer(body)
}

func TestPostBookmarkIntegration(t *testing.T) {
	db := openPostBookmarkIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostMedia{}, &models.PostBookmark{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "bookmark-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "bookmark-other-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "bookmark-author-" + uuid.NewString(), Password: "secret", DisplayName: "Bookmark Author", AvatarURL: "author.jpg"},
		{Username: "bookmark-unavailable-author-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	viewer, otherViewer, author, unavailableAuthor := users[0], users[1], users[2], users[3]

	var postIDs []uint
	var bookmarkUserIDs []uint
	t.Cleanup(func() {
		if len(bookmarkUserIDs) > 0 {
			db.Unscoped().Where("user_id IN ?", bookmarkUserIDs).Delete(&models.PostBookmark{})
		}
		if len(postIDs) > 0 {
			db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBookmark{})
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		userIDs := make([]uint, 0, len(users))
		for _, user := range users {
			if user.ID != 0 {
				userIDs = append(userIDs, user.ID)
			}
		}
		if len(userIDs) > 0 {
			db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
		}
	})
	bookmarkUserIDs = []uint{viewer.ID, otherViewer.ID}

	baseTime := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	createPostForAuthor := func(authorID uint, title, visibility string, createdAt time.Time) models.Post {
		post := models.Post{
			AuthorID:   authorID,
			Content:    title + " body",
			Language:   "und",
			Visibility: visibility,
			Model:      gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
		}
		if err := db.Create(&post).Error; err != nil {
			t.Fatal(err)
		}
		postIDs = append(postIDs, post.ID)
		return post
	}
	createPost := func(title, visibility string, createdAt time.Time) models.Post {
		return createPostForAuthor(author.ID, title, visibility, createdAt)
	}

	publicPost := createPost("public", "public", baseTime)
	parentPost := createPost("parent", "public", baseTime.Add(time.Minute))
	parentID := parentPost.ID
	conversationID := parentPost.ID
	replyPost := models.Post{
		AuthorID: author.ID, Content: "reply body", Language: "und", Visibility: "public",
		ReplyToPostID: &parentID, ConversationID: &conversationID,
		Model: gorm.Model{CreatedAt: baseTime.Add(2 * time.Minute), UpdatedAt: baseTime.Add(2 * time.Minute)},
	}
	if err := db.Create(&replyPost).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, replyPost.ID)
	// The canonical schema only permits public posts. Model an unavailable
	// public post through its deleted author instead of inserting an invalid
	// visibility value that the migration must reject.
	unavailablePost := createPostForAuthor(unavailableAuthor.ID, "private", "public", baseTime.Add(3*time.Minute))
	if err := db.Delete(&unavailableAuthor).Error; err != nil {
		t.Fatal(err)
	}
	deletedPost := createPost("deleted", "public", baseTime.Add(4*time.Minute))
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	otherViewerPost := createPost("other viewer", "public", baseTime.Add(5*time.Minute))
	historyOlder := createPost("history older", "public", baseTime.Add(6*time.Minute))
	historyTieLow := createPost("history tie low", "public", baseTime.Add(7*time.Minute))
	historyTieHigh := createPost("history tie high", "public", baseTime.Add(8*time.Minute))

	if response, status, body := requestBookmarkMutation(t, http.MethodPut, publicPost.ID, viewer.ID); status != http.StatusOK || !response.Bookmarked || body == "" {
		t.Fatalf("first bookmark status=%d body=%s response=%#v", status, body, response)
	}
	if response, status, body := requestBookmarkMutation(t, http.MethodPut, publicPost.ID, viewer.ID); status != http.StatusOK || !response.Bookmarked {
		t.Fatalf("idempotent bookmark status=%d body=%s response=%#v", status, body, response)
	}
	var duplicateCount int64
	if err := db.Model(&models.PostBookmark{}).Where("user_id = ? AND post_id = ?", viewer.ID, publicPost.ID).Count(&duplicateCount).Error; err != nil {
		t.Fatal(err)
	}
	if duplicateCount != 1 {
		t.Fatalf("bookmark rows=%d, want one", duplicateCount)
	}
	if response, status, body := requestBookmarkMutation(t, http.MethodPut, replyPost.ID, viewer.ID); status != http.StatusOK || !response.Bookmarked {
		t.Fatalf("reply bookmark status=%d body=%s response=%#v", status, body, response)
	}
	for _, postID := range []uint{unavailablePost.ID, deletedPost.ID, 999999999} {
		if _, status, body := requestBookmarkMutation(t, http.MethodPut, postID, viewer.ID); status != http.StatusNotFound {
			t.Fatalf("unavailable post=%d status=%d body=%s", postID, status, body)
		}
	}

	states, status, body := requestBookmarkStates(t, viewer.ID, []uint{publicPost.ID, unavailablePost.ID, replyPost.ID, publicPost.ID, deletedPost.ID})
	if status != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", status, body)
	}
	if len(states.Items) != 2 || len(states.UnavailablePostIDs) != 2 {
		t.Fatalf("batch response=%#v", states)
	}
	for _, item := range states.Items {
		if !item.Bookmarked {
			t.Fatalf("expected bookmark item to be true: %#v", item)
		}
	}
	if _, status, _ := requestBookmarkStates(t, otherViewer.ID, []uint{publicPost.ID}); status != http.StatusOK {
		t.Fatalf("other viewer batch status=%d", status)
	}
	otherStates, _, _ := requestBookmarkStates(t, otherViewer.ID, []uint{publicPost.ID})
	if len(otherStates.Items) != 1 || otherStates.Items[0].Bookmarked {
		t.Fatalf("viewer isolation response=%#v", otherStates)
	}

	if response, status, body := requestBookmarkMutation(t, http.MethodDelete, publicPost.ID, viewer.ID); status != http.StatusOK || response.Bookmarked {
		t.Fatalf("unbookmark status=%d body=%s response=%#v", status, body, response)
	}
	if response, status, body := requestBookmarkMutation(t, http.MethodDelete, publicPost.ID, viewer.ID); status != http.StatusOK || response.Bookmarked {
		t.Fatalf("idempotent unbookmark status=%d body=%s response=%#v", status, body, response)
	}

	if err := db.Where("user_id = ?", viewer.ID).Delete(&models.PostBookmark{}).Error; err != nil {
		t.Fatal(err)
	}
	bookmarkAt := baseTime.Add(10 * time.Hour)
	rows := []models.PostBookmark{
		{UserID: viewer.ID, PostID: historyOlder.ID, CreatedAt: bookmarkAt.Add(-time.Hour)},
		{UserID: viewer.ID, PostID: historyTieLow.ID, CreatedAt: bookmarkAt},
		{UserID: viewer.ID, PostID: historyTieHigh.ID, CreatedAt: bookmarkAt},
		{UserID: viewer.ID, PostID: replyPost.ID, CreatedAt: bookmarkAt.Add(time.Hour)},
		{UserID: viewer.ID, PostID: unavailablePost.ID, CreatedAt: bookmarkAt.Add(2 * time.Hour)},
		{UserID: viewer.ID, PostID: deletedPost.ID, CreatedAt: bookmarkAt.Add(3 * time.Hour)},
		{UserID: otherViewer.ID, PostID: otherViewerPost.ID, CreatedAt: bookmarkAt.Add(4 * time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	page1, status, body := requestBookmarkHistory(t, viewer.ID, "limit=2")
	if status != http.StatusOK || len(page1.Items) != 2 || page1.NextCursor == nil {
		t.Fatalf("history page1 status=%d body=%s response=%#v", status, body, page1)
	}
	if page1.Items[0].ID != replyPost.ID || page1.Items[1].ID != historyTieLow.ID {
		t.Fatalf("history page1 ids=%d,%d", page1.Items[0].ID, page1.Items[1].ID)
	}
	if page1.Items[0].ReplyToPostID == nil || page1.Items[0].Content != "reply body" {
		t.Fatalf("reply canonical response=%#v", page1.Items[0])
	}

	page2, status, body := requestBookmarkHistory(t, viewer.ID, "limit=2&cursor="+*page1.NextCursor)
	if status != http.StatusOK || len(page2.Items) != 2 || page2.NextCursor != nil {
		t.Fatalf("history page2 status=%d body=%s response=%#v", status, body, page2)
	}
	if page2.Items[0].ID != historyTieHigh.ID || page2.Items[1].ID != historyOlder.ID {
		t.Fatalf("history page2 ids=%d,%d", page2.Items[0].ID, page2.Items[1].ID)
	}
	if page2.Items[0].ID == page1.Items[0].ID || page2.Items[0].ID == page1.Items[1].ID {
		t.Fatal("history pages contain a duplicate post")
	}
}
