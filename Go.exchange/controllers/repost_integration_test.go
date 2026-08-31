package controllers

import (
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostRepostIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uidx_post_reposts_user_post ON post_reposts (user_id, post_id)").Error; err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	owner := models.User{Username: "repost-owner-" + uuid.NewString(), Password: "secret"}
	viewer := models.User{Username: "repost-viewer-" + uuid.NewString(), Password: "secret"}
	otherViewer := models.User{Username: "repost-other-" + uuid.NewString(), Password: "secret"}
	if err := db.Create(&[]*models.User{&owner, &viewer, &otherViewer}).Error; err != nil {
		t.Fatal(err)
	}
	userIDs := []uint{owner.ID, viewer.ID, otherViewer.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	publishedAt := time.Now().UTC().Add(-time.Minute)
	article := models.Post{
		AuthorID: owner.ID, Content: "Repost integration body", Visibility: "public",
		Model: gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt},
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostArticle{PostID: article.ID, Title: "Repost integration article", Preview: "Repost integration preview", PublicationState: "published", PublishedAt: &publishedAt}).Error; err != nil {
		t.Fatal(err)
	}

	statuses := make(chan int, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			ctx, recorder := newRepostTestContext(http.MethodPut, "/api/posts/"+strconvPostID(article.ID)+"/repost", "", &viewer.ID)
			RepostPost(ctx)
			statuses <- recorder.Code
		}()
	}
	waitGroup.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("concurrent PUT status=%d", status)
		}
	}

	state, err := loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 1 || !state.Reposted {
		t.Fatalf("concurrent same-viewer state=%#v", state)
	}

	ctx, recorder := newRepostTestContext(http.MethodPut, "/api/posts/"+strconvPostID(article.ID)+"/repost", "", &otherViewer.ID)
	RepostPost(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second viewer PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state, err = loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 2 || !state.Reposted {
		t.Fatalf("viewer state after second viewer=%#v", state)
	}
	state, err = loadPostRepostStateWithDB(db, owner.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 2 || state.Reposted {
		t.Fatalf("viewer isolation state=%#v", state)
	}

	ctx, recorder = newRepostTestContext(http.MethodPut, "/api/posts/"+strconvPostID(article.ID)+"/repost", "", &viewer.ID)
	RepostPost(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("duplicate PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state, err = loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil || state.Reposts != 2 || !state.Reposted {
		t.Fatalf("duplicate PUT changed state=%#v err=%v", state, err)
	}

	for index := 0; index < 2; index++ {
		ctx, recorder = newRepostTestContext(http.MethodDelete, "/api/posts/"+strconvPostID(article.ID)+"/repost", "", &viewer.ID)
		UndoRepostPost(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("DELETE %d status=%d body=%s", index+1, recorder.Code, recorder.Body.String())
		}
	}
	state, err = loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil || state.Reposts != 1 || state.Reposted {
		t.Fatalf("idempotent DELETE state=%#v err=%v", state, err)
	}

	expired := time.Now().UTC().Add(-time.Hour)
	if err := db.Model(&models.PostArticle{}).Where("post_id = ?", article.ID).Update("expired_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newRepostTestContext(http.MethodPut, "/api/posts/"+strconvPostID(article.ID)+"/repost", "", &viewer.ID)
	RepostPost(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unavailable PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSoftDeletedReposterExcludedFromRepostStateIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Post{}, &models.PostArticle{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uidx_post_reposts_user_post ON post_reposts (user_id, post_id)").Error; err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "repost-count-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "repost-count-owner-" + uuid.NewString(), Password: "secret"},
		{Username: "repost-count-alice-" + uuid.NewString(), Password: "secret", DisplayName: "Alice"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	viewer, owner, alice := users[0], users[1], users[2]
	userIDs := []uint{viewer.ID, owner.ID, alice.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN (SELECT id FROM posts WHERE author_id IN ?)", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostRepost{})
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: alice.ID}).Error; err != nil {
		t.Fatal(err)
	}

	publishedAt := time.Now().UTC().Add(-time.Minute)
	article := models.Post{
		AuthorID: owner.ID, Content: "Soft-deleted reposter body", Visibility: "public",
		Model: gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt},
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostArticle{PostID: article.ID, Title: "Soft-deleted reposter article", Preview: "Soft-deleted reposter preview", PublicationState: "published", PublishedAt: &publishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	repost := models.PostRepost{UserID: alice.ID, PostID: article.ID, CreatedAt: publishedAt.Add(time.Minute)}
	if err := db.Create(&repost).Error; err != nil {
		t.Fatal(err)
	}

	state, err := loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 1 {
		t.Fatalf("single state before delete=%#v", state)
	}
	states, err := loadPostRepostStatesFromDB(viewer.ID, []uint{article.ID})
	if err != nil {
		t.Fatal(err)
	}
	if states.States[article.ID].Reposts != 1 || len(states.Unavailable) != 0 {
		t.Fatalf("batch state before delete=%#v", states)
	}
	page, status, body := requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != http.StatusOK {
		t.Fatalf("following before delete status=%d body=%s", status, body)
	}
	item := findFollowingTimelineItem(page.Items, article.ID)
	if item == nil || item.ActivityType != followingActivityRepost || item.Actor.ID != alice.ID || item.Post.ID != article.ID {
		t.Fatalf("following before delete item=%#v", item)
	}

	if err := db.Delete(&alice).Error; err != nil {
		t.Fatal(err)
	}
	var deletedAlice models.User
	if err := db.Unscoped().First(&deletedAlice, alice.ID).Error; err != nil || !deletedAlice.DeletedAt.Valid {
		t.Fatalf("alice was not soft-deleted: err=%v deleted_at=%v", err, deletedAlice.DeletedAt)
	}

	state, err = loadPostRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 0 {
		t.Fatalf("single state after delete=%#v", state)
	}
	states, err = loadPostRepostStatesFromDB(viewer.ID, []uint{article.ID})
	if err != nil {
		t.Fatal(err)
	}
	if states.States[article.ID].Reposts != 0 || len(states.Unavailable) != 0 {
		t.Fatalf("batch state after delete=%#v", states)
	}
	var persistedRepost models.PostRepost
	if err := db.Unscoped().Where("user_id = ? AND post_id = ?", alice.ID, article.ID).First(&persistedRepost).Error; err != nil {
		t.Fatalf("repost relation was unexpectedly removed: %v", err)
	}

	page, status, body = requestFollowingTimeline(t, viewer.ID, "limit=50")
	if status != http.StatusOK {
		t.Fatalf("following after delete status=%d body=%s", status, body)
	}
	if findFollowingTimelineItem(page.Items, article.ID) != nil {
		t.Fatalf("soft-deleted Alice activity remained: %v", followingTimelinePostIDs(page.Items))
	}
}
