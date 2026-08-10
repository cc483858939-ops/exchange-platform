package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openFollowingTimelineIntegrationDatabase(t *testing.T) *gorm.DB {
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

func requestFollowingTimeline(t *testing.T, viewerID uint, query string) (followingTimelineResponse, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/api/feed/following"
	if query != "" {
		path += "?" + query
	}
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("user_id", viewerID)
	GetFollowingTimeline(ctx)
	var response followingTimelineResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder.Code, recorder.Body.String()
}

func followingTimelineArticleIDs(items []articleResponse) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func containsArticleID(items []articleResponse, want uint) bool {
	for _, item := range items {
		if item.ID == want {
			return true
		}
	}
	return false
}

func TestFollowingTimelineIntegration(t *testing.T) {
	db := openFollowingTimelineIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Article{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "timeline-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "timeline-followed-a-" + uuid.NewString(), Password: "secret"},
		{Username: "timeline-followed-b-" + uuid.NewString(), Password: "secret"},
		{Username: "timeline-unfollowed-" + uuid.NewString(), Password: "secret"},
		{Username: "timeline-soft-followed-" + uuid.NewString(), Password: "secret"},
		{Username: "timeline-no-follows-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	viewer, followedA, followedB := users[0], users[1], users[2]
	unfollowed, softFollowed, noFollowsViewer := users[3], users[4], users[5]

	userIDs := []uint{viewer.ID, followedA.ID, followedB.ID, unfollowed.ID, softFollowed.ID, noFollowsViewer.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	follows := []models.UserFollow{
		{FollowerID: viewer.ID, FollowingID: followedA.ID},
		{FollowerID: viewer.ID, FollowingID: followedB.ID},
		{FollowerID: viewer.ID, FollowingID: softFollowed.ID},
	}
	if err := db.Create(&follows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&softFollowed).Error; err != nil {
		t.Fatal(err)
	}

	baseTime := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	expiredAt := baseTime.Add(-time.Hour)
	createArticle := func(authorID uint, title, content string, createdAt time.Time, expiredAt *time.Time) models.Article {
		article := models.Article{
			AuthorID:  authorID,
			Title:     title,
			Content:   content,
			Preview:   "preview",
			ExpiredAt: expiredAt,
			Model:     gorm.Model{CreatedAt: createdAt},
		}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		return article
	}

	newerContentOnly := createArticle(followedA.ID, "", "Canonical following body", baseTime.Add(5*time.Minute), nil)
	newerContentOnly.AnalysisState = "pending"
	if err := db.Model(&newerContentOnly).Update("analysis_state", "pending").Error; err != nil {
		t.Fatal(err)
	}
	bTie := createArticle(followedB.ID, "B tie", "B tie body", baseTime.Add(4*time.Minute), nil)
	aTie := createArticle(followedA.ID, "A tie", "A tie body", baseTime.Add(4*time.Minute), nil)
	bOlder := createArticle(followedB.ID, "B older", "B older body", baseTime.Add(3*time.Minute), nil)
	aOlder := createArticle(followedA.ID, "A older", "A older body", baseTime.Add(2*time.Minute), nil)
	expired := createArticle(followedA.ID, "expired", "expired body", baseTime.Add(10*time.Minute), &expiredAt)
	deleted := createArticle(followedA.ID, "deleted", "deleted body", baseTime.Add(11*time.Minute), nil)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	softFollowedPost := createArticle(softFollowed.ID, "soft followed", "soft followed body", baseTime.Add(12*time.Minute), nil)
	unfollowedPost := createArticle(unfollowed.ID, "unfollowed", "unfollowed body", baseTime.Add(13*time.Minute), nil)
	viewerPost := createArticle(viewer.ID, "viewer own", "viewer own body", baseTime.Add(14*time.Minute), nil)
	noFollowsPost := createArticle(noFollowsViewer.ID, "no follows own", "no follows own body", baseTime.Add(15*time.Minute), nil)

	page1, status, body := requestFollowingTimeline(t, viewer.ID, "limit=2")
	if status != http.StatusOK || len(page1.Items) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1 status=%d body=%s response=%#v", status, body, page1)
	}
	if page1.Items[0].ID != newerContentOnly.ID || page1.Items[1].ID != aTie.ID {
		t.Fatalf("page1 order=%v", followingTimelineArticleIDs(page1.Items))
	}
	first := page1.Items[0]
	if first.Title != "" || first.Content != "Canonical following body" || first.AnalysisState != "pending" || first.LikeCount != 0 || first.CommentCount != 0 || first.Author.ID != followedA.ID || first.Author.Username != followedA.Username {
		t.Fatalf("content-only response=%#v", first)
	}

	page2, status, body := requestFollowingTimeline(t, viewer.ID, "limit=2&cursor="+*page1.NextCursor)
	if status != http.StatusOK || len(page2.Items) != 2 || page2.NextCursor == nil {
		t.Fatalf("page2 status=%d body=%s response=%#v", status, body, page2)
	}
	if page2.Items[0].ID != bTie.ID || page2.Items[1].ID != bOlder.ID {
		t.Fatalf("page2 order=%v", followingTimelineArticleIDs(page2.Items))
	}

	page3, status, body := requestFollowingTimeline(t, viewer.ID, "limit=2&cursor="+*page2.NextCursor)
	if status != http.StatusOK || len(page3.Items) != 1 || page3.NextCursor != nil || page3.Items[0].ID != aOlder.ID {
		t.Fatalf("page3 status=%d body=%s response=%#v", status, body, page3)
	}
	allIDs := append(followingTimelineArticleIDs(page1.Items), followingTimelineArticleIDs(page2.Items)...)
	allIDs = append(allIDs, followingTimelineArticleIDs(page3.Items)...)
	expectedIDs := []uint{newerContentOnly.ID, aTie.ID, bTie.ID, bOlder.ID, aOlder.ID}
	if len(allIDs) != len(expectedIDs) {
		t.Fatalf("all ids=%v expected=%v", allIDs, expectedIDs)
	}
	for index, id := range expectedIDs {
		if allIDs[index] != id {
			t.Fatalf("all ids=%v expected=%v", allIDs, expectedIDs)
		}
	}
	for _, excluded := range []uint{expired.ID, deleted.ID, softFollowedPost.ID, unfollowedPost.ID, viewerPost.ID} {
		if containsArticleID(page1.Items, excluded) || containsArticleID(page2.Items, excluded) || containsArticleID(page3.Items, excluded) {
			t.Fatalf("excluded article %d appeared", excluded)
		}
	}

	noFollows, status, body := requestFollowingTimeline(t, noFollowsViewer.ID, "")
	if status != http.StatusOK || noFollows.Items == nil || len(noFollows.Items) != 0 || noFollows.NextCursor != nil || containsArticleID(noFollows.Items, noFollowsPost.ID) {
		t.Fatalf("no follows status=%d body=%s response=%#v", status, body, noFollows)
	}

	activeBeforeFollow, status, body := requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != http.StatusOK || containsArticleID(activeBeforeFollow.Items, unfollowedPost.ID) {
		t.Fatalf("unfollowed post before follow status=%d body=%s response=%#v", status, body, activeBeforeFollow)
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: unfollowed.ID}).Error; err != nil {
		t.Fatal(err)
	}
	activeAfterFollow, status, body := requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != http.StatusOK || !containsArticleID(activeAfterFollow.Items, unfollowedPost.ID) {
		t.Fatalf("unfollowed post after follow status=%d body=%s response=%#v", status, body, activeAfterFollow)
	}
	if err := db.Where("follower_id = ? AND following_id = ?", viewer.ID, unfollowed.ID).Delete(&models.UserFollow{}).Error; err != nil {
		t.Fatal(err)
	}
	activeAfterUnfollow, status, body := requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != http.StatusOK || containsArticleID(activeAfterUnfollow.Items, unfollowedPost.ID) {
		t.Fatalf("unfollowed post after unfollow status=%d body=%s response=%#v", status, body, activeAfterUnfollow)
	}

	if err := db.Delete(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	softDeletedViewer, status, body := requestFollowingTimeline(t, viewer.ID, "")
	if status != http.StatusUnauthorized || softDeletedViewer.Items != nil || body == "" {
		t.Fatalf("soft-deleted viewer status=%d body=%s response=%#v", status, body, softDeletedViewer)
	}
}
