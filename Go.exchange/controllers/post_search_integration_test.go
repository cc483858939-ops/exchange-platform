package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostSearchCriteriaVisibilityTypesAndPaginationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostMedia{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	previousDB := global.APIDb
	global.APIDb = db
	t.Cleanup(func() { global.APIDb = previousDB })
	gin.SetMode(gin.TestMode)

	active := models.User{Username: "post-search-active-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "post-search-other-" + uuid.NewString(), Password: "test"}
	deletedAuthor := models.User{Username: "post-search-deleted-" + uuid.NewString(), Password: "test"}
	for _, user := range []*models.User{&active, &other, &deletedAuthor} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	searchNeedle := "searchneedle" + strings.ReplaceAll(uuid.NewString(), "-", "")
	root := createSearchFixturePost(t, db, active.ID, searchNeedle+" root", now.Add(-3*time.Hour), nil, nil)
	rootID := root.ID
	conversationID := root.ID
	reply := createSearchFixturePost(t, db, active.ID, strings.ToUpper(searchNeedle)+" reply", now.Add(-2*time.Hour), &rootID, &conversationID)
	quote := createSearchFixturePost(t, db, active.ID, searchNeedle+" quote", now.Add(-time.Hour), nil, nil)
	quoteTarget := root.ID
	if err := db.Model(&models.Post{}).Where("id = ?", quote.ID).Update("quote_post_id", quoteTarget).Error; err != nil {
		t.Fatal(err)
	}
	otherPost := createSearchFixturePost(t, db, other.ID, searchNeedle+" other author", now.Add(-30*time.Minute), nil, nil)
	deletedPost := createSearchFixturePost(t, db, active.ID, searchNeedle+" deleted post", now.Add(-20*time.Minute), nil, nil)
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	deletedAuthorPost := createSearchFixturePost(t, db, deletedAuthor.ID, searchNeedle+" deleted author", now.Add(-10*time.Minute), nil, nil)
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	createdPosts := []models.Post{root, reply, quote, otherPost, deletedPost, deletedAuthorPost}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(createdPosts)+1)
		for _, post := range createdPosts {
			ids = append(ids, post.ID)
		}
		if postIDs, err := loadSearchFixturePostIDs(db, ids); err == nil {
			for index := 0; index < len(postIDs); index++ {
				db.Unscoped().Delete(&models.Post{}, postIDs[index])
			}
		}
		for _, user := range []*models.User{&active, &other, &deletedAuthor} {
			if user.ID != 0 {
				db.Unscoped().Delete(&models.User{}, user.ID)
			}
		}
	})

	allQuery := url.Values{"q": {searchNeedle}, "limit": {"2"}}
	first, status, body := requestPostSearchIntegration(t, allQuery)
	if status != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", status, body)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page=%+v, want two items and a cursor", first)
	}
	seen := map[uint]bool{}
	for _, item := range first.Items {
		seen[item.ID] = true
	}
	anchorCursor, err := decodePostSearchCursor(*first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	newer := createSearchFixturePost(t, db, active.ID, searchNeedle+" inserted after page one", anchorCursor.AnchorAt.Add(time.Second), nil, nil)
	createdPosts = append(createdPosts, newer)

	allQuery.Set("cursor", *first.NextCursor)
	second, status, body := requestPostSearchIntegration(t, allQuery)
	if status != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", status, body)
	}
	for _, item := range second.Items {
		if seen[item.ID] {
			t.Fatalf("post %d was duplicated across pages", item.ID)
		}
		seen[item.ID] = true
	}
	if second.NextCursor != nil {
		t.Fatalf("second page cursor=%q, want nil", *second.NextCursor)
	}
	if len(seen) != 4 || seen[root.ID] != true || seen[reply.ID] != true || seen[quote.ID] != true || seen[otherPost.ID] != true {
		t.Fatalf("visible search IDs=%v, want root, Reply, Quote, and other author", seen)
	}
	if seen[deletedPost.ID] || seen[deletedAuthorPost.ID] || seen[newer.ID] {
		t.Fatalf("search included deleted content or a Post newer than its anchor: %v", seen)
	}

	filtered := url.Values{"q": {searchNeedle}, "author_id": {strconv.FormatUint(uint64(active.ID), 10)}, "limit": {"50"}}
	filteredPage, status, body := requestPostSearchIntegration(t, filtered)
	if status != http.StatusOK {
		t.Fatalf("author filter status=%d body=%s", status, body)
	}
	for _, item := range filteredPage.Items {
		if item.Author.ID != active.ID {
			t.Fatalf("author filter returned author %d, want %d", item.Author.ID, active.ID)
		}
	}
	if len(filteredPage.Items) != 3 {
		t.Fatalf("author-filtered result count=%d, want 3", len(filteredPage.Items))
	}
	unknown := url.Values{"q": {searchNeedle}, "author_id": {"9007199254740000"}}
	unknownPage, status, body := requestPostSearchIntegration(t, unknown)
	if status != http.StatusOK || len(unknownPage.Items) != 0 || unknownPage.NextCursor != nil {
		t.Fatalf("unknown-author result status=%d page=%+v body=%s", status, unknownPage, body)
	}

	from := url.Values{
		"q": {searchNeedle}, "from": {root.CreatedAt.UTC().Format(time.RFC3339Nano)},
		"to": {reply.CreatedAt.UTC().Format(time.RFC3339Nano)},
	}
	datePage, status, body := requestPostSearchIntegration(t, from)
	if status != http.StatusOK || len(datePage.Items) != 1 || datePage.Items[0].ID != root.ID {
		t.Fatalf("inclusive/exclusive time result status=%d page=%+v body=%s", status, datePage, body)
	}

	for _, literal := range []struct {
		query   string
		content string
		other   string
	}{
		{query: "50%", content: "literal 50% result", other: "50x"},
		{query: "a_b", content: "literal a_b result", other: "axb"},
		{query: `a\b`, content: `literal a\b result`, other: "ab"},
	} {
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
		query := literal.query + suffix
		needleContent := strings.Replace(literal.content, literal.query, query, 1)
		otherContent := strings.Replace(literal.content, literal.query, literal.other+suffix, 1)
		needlePost := createSearchFixturePost(t, db, active.ID, needleContent, time.Now().UTC(), nil, nil)
		otherNeedle := createSearchFixturePost(t, db, active.ID, otherContent, time.Now().UTC().Add(-time.Second), nil, nil)
		createdPosts = append(createdPosts, needlePost, otherNeedle)
		page, status, body := requestPostSearchIntegration(t, url.Values{"q": {query}, "limit": {"50"}})
		if status != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != needlePost.ID {
			t.Fatalf("literal query %q status=%d page=%+v body=%s", literal.query, status, page, body)
		}
	}
}

func createSearchFixturePost(t *testing.T, db *gorm.DB, authorID uint, content string, createdAt time.Time, replyTo, conversationID *uint) models.Post {
	t.Helper()
	post := models.Post{
		AuthorID: authorID, Content: content, Visibility: "public",
		ReplyToPostID: replyTo, ConversationID: conversationID,
	}
	post.CreatedAt = createdAt
	post.UpdatedAt = createdAt
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	return post
}

func loadSearchFixturePostIDs(db *gorm.DB, ids []uint) ([]uint, error) {
	var postIDs []uint
	err := db.Unscoped().Model(&models.Post{}).Where("id IN ?", ids).Order("id DESC").Pluck("id", &postIDs).Error
	return postIDs, err
}

func requestPostSearchIntegration(t *testing.T, query url.Values) (postPageResponse, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/posts/search?"+query.Encode(), nil)
	SearchPosts(ctx)
	var page postPageResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode Post search response: %v body=%s", err, recorder.Body.String())
		}
	}
	return page, recorder.Code, recorder.Body.String()
}
