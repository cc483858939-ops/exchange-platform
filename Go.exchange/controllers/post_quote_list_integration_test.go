package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type quoteListIntegrationFixture struct {
	db          *gorm.DB
	users       []models.User
	targets     []models.Post
	childPosts  []models.Post
	Target      models.Post
	Another     models.Post
	Author      models.User
	Quoter      models.User
	OtherQuoter models.User
}

func newQuoteListIntegrationFixture(t *testing.T, db *gorm.DB) *quoteListIntegrationFixture {
	t.Helper()
	fixture := &quoteListIntegrationFixture{db: db}
	fixture.Author = fixture.createUser(t, "quote-list-author-")
	fixture.Quoter = fixture.createUser(t, "quote-list-quoter-")
	fixture.OtherQuoter = fixture.createUser(t, "quote-list-other-quoter-")
	fixture.Target = fixture.createTarget(t, fixture.Author.ID, "quote list target")
	fixture.Another = fixture.createTarget(t, fixture.Author.ID, "another quote list target")
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *quoteListIntegrationFixture) createUser(t *testing.T, prefix string) models.User {
	t.Helper()
	user := models.User{Username: prefix + uuid.NewString(), Password: "test"}
	if err := f.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	f.users = append(f.users, user)
	return user
}

func (f *quoteListIntegrationFixture) createTarget(t *testing.T, authorID uint, content string) models.Post {
	t.Helper()
	now := time.Now().UTC()
	post := models.Post{Model: gorm.Model{CreatedAt: now, UpdatedAt: now}, AuthorID: authorID, Content: content, Visibility: "public"}
	if err := f.db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	f.targets = append(f.targets, post)
	return post
}

func (f *quoteListIntegrationFixture) createPost(t *testing.T, authorID uint, content string, createdAt time.Time, quoteID, replyID *uint) models.Post {
	t.Helper()
	post := models.Post{
		Model:    gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
		AuthorID: authorID, Content: content, Language: "en", Visibility: "public",
		QuotePostID: quoteID, ReplyToPostID: replyID,
	}
	if err := f.db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	f.childPosts = append(f.childPosts, post)
	return post
}

func (f *quoteListIntegrationFixture) cleanup() {
	postIDs := make([]uint, 0, len(f.childPosts)+len(f.targets))
	for _, post := range f.childPosts {
		postIDs = append(postIDs, post.ID)
	}
	for _, post := range f.targets {
		postIDs = append(postIDs, post.ID)
	}
	if len(postIDs) > 0 {
		f.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostRepost{})
		f.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
		f.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostMedia{})
		f.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
	}
	childIDs := make([]uint, 0, len(f.childPosts))
	for _, post := range f.childPosts {
		childIDs = append(childIDs, post.ID)
	}
	if len(childIDs) > 0 {
		f.db.Unscoped().Where("id IN ?", childIDs).Delete(&models.Post{})
	}
	targetIDs := make([]uint, 0, len(f.targets))
	for _, post := range f.targets {
		targetIDs = append(targetIDs, post.ID)
	}
	if len(targetIDs) > 0 {
		f.db.Unscoped().Where("id IN ?", targetIDs).Delete(&models.Post{})
	}
	userIDs := make([]uint, 0, len(f.users))
	for _, user := range f.users {
		userIDs = append(userIDs, user.ID)
	}
	if len(userIDs) > 0 {
		f.db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfileDirty{})
		f.db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostBehavior{})
		f.db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func quoteListContext(targetID uint, query string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/posts/"+strconvUint(targetID)+"/quotes"+query, nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconvUint(targetID)}}
	return ctx, recorder
}

func TestGetPostQuotesVisibilityHydrationAndReferenceBatchIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newQuoteListIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := fixture.createPost(t, fixture.Quoter.ID, "first public quote", now, &fixture.Target.ID, nil)
	second := fixture.createPost(t, fixture.OtherQuoter.ID, "second public quote", now.Add(-time.Second), &fixture.Target.ID, nil)
	deletedQuote := fixture.createPost(t, fixture.Quoter.ID, "deleted quote", now.Add(-2*time.Second), &fixture.Target.ID, nil)
	if err := db.Delete(&deletedQuote).Error; err != nil {
		t.Fatal(err)
	}
	deletedAuthor := fixture.createUser(t, "quote-list-deleted-author-")
	deletedAuthorQuote := fixture.createPost(t, deletedAuthor.ID, "deleted author's quote", now.Add(-3*time.Second), &fixture.Target.ID, nil)
	otherTargetQuote := fixture.createPost(t, fixture.OtherQuoter.ID, "different target quote", now.Add(-4*time.Second), &fixture.Another.ID, nil)
	plainPost := fixture.createPost(t, fixture.Quoter.ID, "plain post", now.Add(-5*time.Second), nil, nil)
	reply := createReplyRecord(t, db, fixture.Target.ID, fixture.OtherQuoter.ID, "reply, not quote", now.Add(-6*time.Second))
	fixture.childPosts = append(fixture.childPosts, reply)
	fixture.childPosts = append(fixture.childPosts, deletedAuthorQuote, otherTargetQuote, plainPost)
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostMedia{PostID: first.ID, MediaType: "image", URL: "quote.jpg", LargeURL: "quote-large.jpg", Width: 80, Height: 60, Position: 0, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&fixture.Target).UpdateColumn("quote_count", 0).Error; err != nil {
		t.Fatal(err)
	}

	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	originalDB := global.APIDb
	global.APIDb = db.Session(&gorm.Session{Logger: queryLogger})
	t.Cleanup(func() { global.APIDb = originalDB })
	ctx, recorder := quoteListContext(fixture.Target.ID, "?limit=50")
	GetPostQuotes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response quoteListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.NextCursor != nil {
		t.Fatalf("response=%#v", response)
	}
	if response.Items[0].ID != first.ID || response.Items[1].ID != second.ID {
		t.Fatalf("quote IDs=%d,%d want %d,%d", response.Items[0].ID, response.Items[1].ID, first.ID, second.ID)
	}
	for _, item := range response.Items {
		if item.QuotePostID == nil || *item.QuotePostID != fixture.Target.ID || item.QuotePost == nil || item.QuotePost.ID != fixture.Target.ID || item.QuotePost.Deleted {
			t.Fatalf("quote reference=%#v quote_post_id=%v", item.QuotePost, item.QuotePostID)
		}
		if item.Media == nil || item.LikeCount < 0 || item.RepostCount < 0 || item.ReplyCount < 0 || item.QuoteCount < 0 || item.ViewCount < 0 {
			t.Fatalf("quote response omitted standard Post hydration: %#v", item)
		}
	}
	if len(response.Items[0].Media) != 1 || response.Items[0].Media[0].URL != "quote.jpg" {
		t.Fatalf("quote media=%#v", response.Items[0].Media)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["total"]; exists {
		t.Fatalf("response must not include total: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "deleted author's quote") || strings.Contains(recorder.Body.String(), "different target quote") || strings.Contains(recorder.Body.String(), "reply, not quote") {
		t.Fatalf("ineligible Posts leaked into response: %s", recorder.Body.String())
	}
	assertOneReferenceBatch(t, queryLogger.snapshot(), 3, 2, 2, 1)
}

func TestGetPostQuotesCursorOrderingAndNewQuoteStabilityIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newQuoteListIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	older := fixture.createPost(t, fixture.Quoter.ID, "older", now.Add(-2*time.Second), &fixture.Target.ID, nil)
	equalFirst := fixture.createPost(t, fixture.Quoter.ID, "equal first", now, &fixture.Target.ID, nil)
	equalSecond := fixture.createPost(t, fixture.OtherQuoter.ID, "equal second", now, &fixture.Target.ID, nil)
	oldest := fixture.createPost(t, fixture.OtherQuoter.ID, "oldest", now.Add(-3*time.Second), &fixture.Target.ID, nil)

	ctx, recorder := quoteListContext(fixture.Target.ID, "?limit=2")
	GetPostQuotes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var firstPage quoteListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 2 || firstPage.NextCursor == nil {
		t.Fatalf("first page=%#v", firstPage)
	}
	if firstPage.Items[0].ID != equalSecond.ID || firstPage.Items[1].ID != equalFirst.ID {
		t.Fatalf("first page IDs=%d,%d want higher equal-time ID %d before %d", firstPage.Items[0].ID, firstPage.Items[1].ID, equalSecond.ID, equalFirst.ID)
	}
	inserted := fixture.createPost(t, fixture.Quoter.ID, "created after page one", now.Add(time.Second), &fixture.Target.ID, nil)
	ctx, recorder = quoteListContext(fixture.Target.ID, "?limit=2&cursor="+*firstPage.NextCursor)
	GetPostQuotes(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var secondPage quoteListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Items) != 2 || secondPage.NextCursor != nil {
		t.Fatalf("second page=%#v", secondPage)
	}
	if secondPage.Items[0].ID != older.ID || secondPage.Items[1].ID != oldest.ID {
		t.Fatalf("second page IDs=%d,%d want %d,%d", secondPage.Items[0].ID, secondPage.Items[1].ID, older.ID, oldest.ID)
	}
	for _, item := range append(firstPage.Items, secondPage.Items...) {
		if item.ID == inserted.ID {
			t.Fatalf("newer Quote %d appeared in cursor continuation", inserted.ID)
		}
	}
}

func TestGetPostQuotesReturnsNotFoundWhenTargetUnavailableIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	gin.SetMode(gin.TestMode)
	db := openReplyIntegrationDatabase(t)
	fixture := newQuoteListIntegrationFixture(t, db)
	fixture.createPost(t, fixture.Quoter.ID, "quote that should not be listed", time.Now().UTC(), &fixture.Target.ID, nil)
	if err := db.Delete(&fixture.Target).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder := quoteListContext(fixture.Target.ID, "")
	GetPostQuotes(ctx)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "post not found") {
		t.Fatalf("status=%d body=%s, want normal Post not-found response", recorder.Code, recorder.Body.String())
	}
}
