package controllers

import (
	"os"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestFollowingRepostActivityIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Article{}, &models.ArticleRepost{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uidx_article_reposts_user_article ON article_reposts (user_id, article_id)").Error; err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "repost-activity-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "repost-activity-direct-" + uuid.NewString(), Password: "secret", DisplayName: "Direct Author"},
		{Username: "repost-activity-alice-" + uuid.NewString(), Password: "secret", DisplayName: "Alice"},
		{Username: "repost-activity-bob-" + uuid.NewString(), Password: "secret", DisplayName: "Bob"},
		{Username: "repost-activity-charlie-" + uuid.NewString(), Password: "secret", DisplayName: "Charlie"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	viewer, directAuthor, alice, bob, charlie := users[0], users[1], users[2], users[3], users[4]
	userIDs := []uint{viewer.ID, directAuthor.ID, alice.ID, bob.ID, charlie.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id IN (SELECT id FROM articles WHERE author_id IN ?)", userIDs).Delete(&models.ArticleRepost{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.ArticleRepost{})
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})
	if err := db.Create([]models.UserFollow{
		{FollowerID: viewer.ID, FollowingID: directAuthor.ID},
		{FollowerID: viewer.ID, FollowingID: alice.ID},
		{FollowerID: viewer.ID, FollowingID: charlie.ID},
	}).Error; err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	createArticle := func(authorID uint, title string, publishedAt time.Time) models.Article {
		article := models.Article{
			AuthorID:         authorID,
			Title:            title,
			Content:          title + " body",
			Preview:          title + " preview",
			PublicationState: consts.ArticlePublicationStatePublished,
			PublishedAt:      &publishedAt,
			Model:            gorm.Model{CreatedAt: publishedAt},
		}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		return article
	}
	createRepost := func(userID, articleID uint, createdAt time.Time) models.ArticleRepost {
		repost := models.ArticleRepost{UserID: userID, ArticleID: articleID, CreatedAt: createdAt}
		if err := db.Create(&repost).Error; err != nil {
			t.Fatal(err)
		}
		return repost
	}

	directArticle := createArticle(directAuthor.ID, "Direct post", base.Add(1*time.Minute))
	bobArticle := createArticle(bob.ID, "Bob canonical post", base.Add(2*time.Minute))
	aliceBobRepost := createRepost(alice.ID, bobArticle.ID, base.Add(3*time.Minute))
	charlieBobRepost := createRepost(charlie.ID, bobArticle.ID, base.Add(4*time.Minute))
	directAndReposted := createArticle(directAuthor.ID, "Direct then reposted", base.Add(5*time.Minute))
	aliceDirectRepost := createRepost(alice.ID, directAndReposted.ID, base.Add(6*time.Minute))
	tieTime := base.Add(7 * time.Minute)
	tieArticle := createArticle(directAuthor.ID, "Tie repost", tieTime)
	tieRepost := createRepost(alice.ID, tieArticle.ID, tieTime)
	tieArticleA := createArticle(directAuthor.ID, "Tie A", tieTime)
	tieArticleB := createArticle(directAuthor.ID, "Tie B", tieTime)
	deletedArticle := createArticle(bob.ID, "Deleted canonical", base.Add(8*time.Minute))
	createRepost(charlie.ID, deletedArticle.ID, base.Add(9*time.Minute))
	if err := db.Delete(&deletedArticle).Error; err != nil {
		t.Fatal(err)
	}

	page, status, body := requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	bobItem := findFollowingTimelineItem(page.Items, bobArticle.ID)
	if bobItem == nil || bobItem.ActivityType != followingActivityRepost || bobItem.Actor.ID != charlie.ID || bobItem.Article.Author.ID != bob.ID {
		t.Fatalf("latest repost item=%#v", bobItem)
	}
	tieItem := findFollowingTimelineItem(page.Items, tieArticle.ID)
	if tieItem == nil || tieItem.ActivityType != followingActivityRepost || tieItem.SourceID != tieRepost.ID {
		t.Fatalf("equal timestamp rank item=%#v", tieItem)
	}
	if findFollowingTimelineItem(page.Items, deletedArticle.ID) != nil {
		t.Fatal("deleted canonical article appeared")
	}

	pageOne, status, body := requestFollowingTimeline(t, viewer.ID, "limit=2")
	if status != 200 || len(pageOne.Items) != 2 || pageOne.NextCursor == nil {
		t.Fatalf("page one status=%d body=%s response=%#v", status, body, pageOne)
	}
	allIDs := followingTimelineArticleIDs(pageOne.Items)
	cursor := *pageOne.NextCursor
	for pageNumber := 2; pageNumber <= 10 && cursor != ""; pageNumber++ {
		next, nextStatus, nextBody := requestFollowingTimeline(t, viewer.ID, "limit=2&cursor="+cursor)
		if nextStatus != 200 {
			t.Fatalf("page %d status=%d body=%s", pageNumber, nextStatus, nextBody)
		}
		allIDs = append(allIDs, followingTimelineArticleIDs(next.Items)...)
		if next.NextCursor == nil {
			cursor = ""
		} else {
			cursor = *next.NextCursor
		}
	}
	seen := make(map[uint]struct{}, len(allIDs))
	for _, id := range allIDs {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate canonical article across pages: %v", allIDs)
		}
		seen[id] = struct{}{}
	}
	for _, id := range []uint{directArticle.ID, bobArticle.ID, directAndReposted.ID, tieArticle.ID, tieArticleA.ID, tieArticleB.ID} {
		if _, exists := seen[id]; !exists {
			t.Fatalf("article %d missing from mixed pagination: %v", id, allIDs)
		}
	}

	if err := db.Where("id = ?", charlieBobRepost.ID).Delete(&models.ArticleRepost{}).Error; err != nil {
		t.Fatal(err)
	}
	page, status, body = requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != 200 {
		t.Fatalf("undo newest status=%d body=%s", status, body)
	}
	bobItem = findFollowingTimelineItem(page.Items, bobArticle.ID)
	if bobItem == nil || bobItem.Actor.ID != alice.ID || bobItem.SourceID != aliceBobRepost.ID {
		t.Fatalf("undo newest did not reveal previous repost=%#v", bobItem)
	}

	if err := db.Where("id = ?", aliceDirectRepost.ID).Delete(&models.ArticleRepost{}).Error; err != nil {
		t.Fatal(err)
	}
	page, status, body = requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != 200 {
		t.Fatalf("direct fallback status=%d body=%s", status, body)
	}
	directItem := findFollowingTimelineItem(page.Items, directAndReposted.ID)
	if directItem == nil || directItem.ActivityType != followingActivityPost || directItem.Actor.ID != directAuthor.ID {
		t.Fatalf("undo did not reveal direct activity=%#v", directItem)
	}

	if err := db.Where("follower_id = ? AND following_id = ?", viewer.ID, alice.ID).Delete(&models.UserFollow{}).Error; err != nil {
		t.Fatal(err)
	}
	page, status, body = requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != 200 {
		t.Fatalf("unfollow status=%d body=%s", status, body)
	}
	if findFollowingTimelineItem(page.Items, bobArticle.ID) != nil || findFollowingTimelineItem(page.Items, tieArticle.ID) != nil {
		t.Fatalf("Alice activities remained after unfollow: %v", followingTimelineArticleIDs(page.Items))
	}
}
