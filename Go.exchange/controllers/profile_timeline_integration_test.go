package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func profileTimelineRequest(t *testing.T, userID uint, query string) (timelinePageResponse, int, string) {
	t.Helper()
	path := "/api/users/" + strconvUint(userID) + "/timeline"
	if query != "" {
		path += "?" + query
	}
	ctx, recorder := newUserControllerContext(path, strconvUint(userID))
	GetUserTimeline(ctx)
	var response timelinePageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		return timelinePageResponse{}, recorder.Code, recorder.Body.String()
	}
	return response, recorder.Code, recorder.Body.String()
}

func openProfileTimelineIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
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
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uidx_post_reposts_user_post ON post_reposts (user_id, post_id)").Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestProfileTimelineActivityIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "profile-timeline-target-" + uuid.NewString(), Password: "secret"},
		{Username: "profile-timeline-author-a-" + uuid.NewString(), Password: "secret"},
		{Username: "profile-timeline-author-b-" + uuid.NewString(), Password: "secret"},
		{Username: "profile-timeline-deleted-author-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	profileUser, authorA, authorB, deletedAuthor := users[0], users[1], users[2], users[3]
	userIDs := []uint{profileUser.ID, authorA.ID, authorB.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN (SELECT id FROM posts WHERE author_id IN ?)", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	base := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	createPost := func(authorID uint, content string, createdAt time.Time, visibility string, replyTo, quote *uint) models.Post {
		item := models.Post{
			AuthorID: authorID, Content: content, Visibility: visibility,
			ReplyToPostID: replyTo, QuotePostID: quote,
			Model: gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		return item
	}
	createRepost := func(postID uint, createdAt time.Time) models.PostRepost {
		item := models.PostRepost{UserID: profileUser.ID, PostID: postID, CreatedAt: createdAt}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		return item
	}

	aCanonical := createPost(authorA.ID, "Author A canonical", base.Add(-24*time.Hour), "public", nil, nil)
	bCanonical := createPost(authorB.ID, "Author B canonical", base.Add(-48*time.Hour), "public", nil, nil)
	ownA := createPost(profileUser.ID, "Profile authored A", base, "public", nil, nil)
	repostA := createRepost(aCanonical.ID, base.Add(10*time.Minute))
	quote := createPost(profileUser.ID, "Profile quote", base.Add(15*time.Minute), "public", nil, &aCanonical.ID)
	ownB := createPost(profileUser.ID, "Profile authored B", base.Add(20*time.Minute), "public", nil, nil)
	repostB := createRepost(bCanonical.ID, base.Add(30*time.Minute))
	replyParent := createPost(authorA.ID, "Reply parent", base.Add(-2*time.Hour), "public", nil, nil)
	_ = createPost(profileUser.ID, "Profile reply excluded", base.Add(25*time.Minute), "public", &replyParent.ID, nil)

	deletedCanonical := createPost(authorA.ID, "Deleted canonical", base.Add(-3*time.Hour), "public", nil, nil)
	createRepost(deletedCanonical.ID, base.Add(40*time.Minute))
	if err := db.Delete(&deletedCanonical).Error; err != nil {
		t.Fatal(err)
	}
	nonPublicCanonical := createPost(authorB.ID, "Private canonical", base.Add(-4*time.Hour), "private", nil, nil)
	createRepost(nonPublicCanonical.ID, base.Add(41*time.Minute))
	deletedAuthorCanonical := createPost(deletedAuthor.ID, "Deleted author canonical", base.Add(-5*time.Hour), "public", nil, nil)
	createRepost(deletedAuthorCanonical.ID, base.Add(42*time.Minute))
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	page, status, body := profileTimelineRequest(t, profileUser.ID, "limit=50")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	want := []struct {
		activityType timelineActivityType
		activityAt   time.Time
		sourceID     uint
		postID       uint
		authorID     uint
	}{
		{timelineActivityRepost, base.Add(30 * time.Minute), repostB.ID, bCanonical.ID, authorB.ID},
		{timelineActivityPost, base.Add(20 * time.Minute), ownB.ID, ownB.ID, profileUser.ID},
		{timelineActivityPost, base.Add(15 * time.Minute), quote.ID, quote.ID, profileUser.ID},
		{timelineActivityRepost, base.Add(10 * time.Minute), repostA.ID, aCanonical.ID, authorA.ID},
		{timelineActivityPost, base, ownA.ID, ownA.ID, profileUser.ID},
	}
	if len(page.Items) != len(want) || page.NextCursor != nil {
		t.Fatalf("unexpected profile timeline=%#v", page)
	}
	for index, expected := range want {
		item := page.Items[index]
		if item.ActivityType != expected.activityType || !item.ActivityAt.Equal(expected.activityAt) || item.SourceID != expected.sourceID || item.Post.ID != expected.postID || item.Post.Author.ID != expected.authorID || item.Actor.ID != profileUser.ID {
			t.Fatalf("item %d=%#v want type=%q at=%s source=%d post=%d author=%d actor=%d", index, item, expected.activityType, expected.activityAt, expected.sourceID, expected.postID, expected.authorID, profileUser.ID)
		}
	}
	if page.Items[2].Post.QuotePost == nil || page.Items[2].Post.QuotePost.ID != aCanonical.ID {
		t.Fatalf("quote reference was not hydrated: %#v", page.Items[2].Post.QuotePost)
	}
	for _, item := range page.Items {
		if item.Post.Content == "Profile reply excluded" || item.Post.Content == "Deleted canonical" || item.Post.Content == "Private canonical" || item.Post.Content == "Deleted author canonical" {
			t.Fatalf("ineligible activity appeared: %#v", item)
		}
	}

	if _, err := mutatePostRepostFromDB(profileUser.ID, bCanonical.ID, false); err != nil {
		t.Fatal(err)
	}
	page, status, body = profileTimelineRequest(t, profileUser.ID, "limit=50")
	if status != http.StatusOK {
		t.Fatalf("undo status=%d body=%s", status, body)
	}
	for _, item := range page.Items {
		if item.SourceID == repostB.ID || item.Post.ID == bCanonical.ID {
			t.Fatalf("undone repost remained: %#v", item)
		}
	}
	if len(page.Items) != len(want)-1 {
		t.Fatalf("authored timeline activity disappeared with repost: %#v", page.Items)
	}
}

func TestProfileTimelineCursorIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "profile-cursor-target-" + uuid.NewString(), Password: "secret"},
		{Username: "profile-cursor-author-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	profileUser, canonicalAuthor := users[0], users[1]
	userIDs := []uint{profileUser.ID, canonicalAuthor.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN (SELECT id FROM posts WHERE author_id IN ?)", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	stamp := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	activities := make([]timelineCursor, 0, 16)
	for index := 0; index < 8; index++ {
		post := models.Post{
			AuthorID: profileUser.ID, Content: "cursor post", Visibility: "public",
			Model: gorm.Model{CreatedAt: stamp, UpdatedAt: stamp},
		}
		if err := db.Create(&post).Error; err != nil {
			t.Fatal(err)
		}
		activities = append(activities, timelineCursor{ActivityAt: stamp, ActivityType: string(timelineActivityPost), SourceID: post.ID})
	}
	for index := 0; index < 8; index++ {
		canonical := models.Post{
			AuthorID: canonicalAuthor.ID, Content: "cursor canonical", Visibility: "public",
			Model: gorm.Model{CreatedAt: stamp.Add(-time.Hour), UpdatedAt: stamp.Add(-time.Hour)},
		}
		if err := db.Create(&canonical).Error; err != nil {
			t.Fatal(err)
		}
		repost := models.PostRepost{UserID: profileUser.ID, PostID: canonical.ID, CreatedAt: stamp}
		if err := db.Create(&repost).Error; err != nil {
			t.Fatal(err)
		}
		activities = append(activities, timelineCursor{ActivityAt: stamp, ActivityType: string(timelineActivityRepost), SourceID: repost.ID})
	}
	sort.Slice(activities, func(i, j int) bool {
		leftRank := timelineActivityRank(activities[i].ActivityType)
		rightRank := timelineActivityRank(activities[j].ActivityType)
		if !activities[i].ActivityAt.Equal(activities[j].ActivityAt) {
			return activities[i].ActivityAt.After(activities[j].ActivityAt)
		}
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return activities[i].SourceID > activities[j].SourceID
	})

	var received []timelineCursor
	var cursor string
	for pageNumber := 0; pageNumber < 8; pageNumber++ {
		query := "limit=5"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		page, status, body := profileTimelineRequest(t, profileUser.ID, query)
		if status != http.StatusOK {
			t.Fatalf("page %d status=%d body=%s", pageNumber+1, status, body)
		}
		for _, item := range page.Items {
			received = append(received, timelineCursor{
				ActivityAt:   item.ActivityAt,
				ActivityType: string(item.ActivityType),
				SourceID:     item.SourceID,
			})
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(received) != len(activities) {
		t.Fatalf("received %d activities, want %d: %#v", len(received), len(activities), received)
	}
	seen := make(map[string]struct{}, len(received))
	for index, item := range received {
		key := item.ActivityType + ":" + strconvUint(item.SourceID)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate activity %q", key)
		}
		seen[key] = struct{}{}
		if item != activities[index] {
			t.Fatalf("activity %d=%#v want %#v", index, item, activities[index])
		}
	}

	for _, invalid := range []string{"", "not-base64"} {
		_, status, body := profileTimelineRequest(t, profileUser.ID, "cursor="+invalid)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid cursor %q status=%d body=%s", invalid, status, body)
		}
	}
	invalidType, err := encodeTimelineCursor(timelineCursor{ActivityAt: stamp, ActivityType: "invalid", SourceID: 1})
	if err == nil {
		t.Fatal("invalid activity type unexpectedly encoded")
	}
	_ = invalidType
}
