package controllers

import (
	"encoding/json"
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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostMedia{}, &models.PostRepost{}); err != nil {
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

}

func TestPublicPostRepostCountHydrationAndCacheFreshnessIntegration(t *testing.T) {
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

	originalDB, originalRedis, originalCacheLoader := global.Db, global.RedisDB, loadPostDetailCache
	global.Db = db
	global.RedisDB = nil
	t.Cleanup(func() {
		global.Db = originalDB
		global.RedisDB = originalRedis
		loadPostDetailCache = originalCacheLoader
	})

	users := []models.User{
		{Username: "public-count-owner-" + uuid.NewString(), Password: "secret"},
		{Username: "public-count-alice-" + uuid.NewString(), Password: "secret"},
		{Username: "public-count-bob-" + uuid.NewString(), Password: "secret"},
		{Username: "public-count-deleted-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	owner, alice, bob, deleted := users[0], users[1], users[2], users[3]
	postIDs := make([]uint, 0, 1)
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ? OR user_id IN ?", postIDs, []uint{owner.ID, alice.ID, bob.ID, deleted.ID}).Delete(&models.PostRepost{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, alice.ID, bob.ID, deleted.ID}).Delete(&models.User{})
	})

	now := time.Now().UTC().Add(-time.Minute)
	article := models.Post{
		Model:    gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID: owner.ID, Content: "public repost count", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, article.ID)
	if err := db.Create(&[]models.PostRepost{
		{UserID: alice.ID, PostID: article.ID, CreatedAt: now},
		{UserID: bob.ID, PostID: article.ID, CreatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	var cached postResponse
	cachePrimed := false
	loadPostDetailCache = func(_ string, loader func() (postResponse, error)) (postResponse, error) {
		if cachePrimed {
			return cached, nil
		}
		response, err := loader()
		if err != nil {
			return postResponse{}, err
		}
		cached = response
		cachePrimed = true
		return response, nil
	}

	first, err := loadPostDetail(strconvPostID(article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if first.RepostCount != 2 {
		t.Fatalf("initial public repost_count=%d want 2", first.RepostCount)
	}

	if err := db.Create(&models.PostRepost{UserID: deleted.ID, PostID: article.ID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := loadPostDetail(strconvPostID(article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if second.RepostCount != 3 {
		t.Fatalf("warm-cache public repost_count=%d want 3", second.RepostCount)
	}

	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	third, err := loadPostDetail(strconvPostID(article.ID))
	if err != nil {
		t.Fatal(err)
	}
	if third.RepostCount != 2 {
		t.Fatalf("deleted-reposter public repost_count=%d want 2", third.RepostCount)
	}

	ctx, recorder := newReplyIntegrationContext(http.MethodGet, "/api/posts/"+strconvPostID(article.ID), strconvPostID(article.ID), "", 0)
	GetPostByID(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("public post GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		RepostCount int64 `json:"repost_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RepostCount != 2 {
		t.Fatalf("public post JSON repost_count=%d want 2", payload.RepostCount)
	}
}

func TestPublicPostRepostCountsHydrateTimelineRecommendationsAndRepliesIntegration(t *testing.T) {
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
	originalDB, originalRedis := global.Db, global.RedisDB
	global.Db = db
	global.RedisDB = nil
	t.Cleanup(func() {
		global.Db = originalDB
		global.RedisDB = originalRedis
	})

	users := []models.User{
		{Username: "surface-count-owner-" + uuid.NewString(), Password: "secret"},
		{Username: "surface-count-alice-" + uuid.NewString(), Password: "secret"},
		{Username: "surface-count-bob-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	owner, alice, bob := users[0], users[1], users[2]
	postIDs := make([]uint, 0, 2)
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ? OR user_id IN ?", postIDs, []uint{owner.ID, alice.ID, bob.ID}).Delete(&models.PostRepost{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, alice.ID, bob.ID}).Delete(&models.User{})
	})

	now := time.Now().UTC().Add(-time.Hour)
	root := models.Post{
		Model:    gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID: owner.ID, Content: "timeline root", Visibility: "public",
	}
	if err := db.Create(&root).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, root.ID)
	conversationID := root.ID
	replyToID := root.ID
	reply := models.Post{
		Model:          gorm.Model{CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)},
		AuthorID:       owner.ID,
		Content:        "public reply",
		Visibility:     "public",
		ConversationID: &conversationID,
		ReplyToPostID:  &replyToID,
	}
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, reply.ID)
	if err := db.Create(&[]models.PostRepost{
		{UserID: alice.ID, PostID: root.ID, CreatedAt: now},
		{UserID: bob.ID, PostID: root.ID, CreatedAt: now},
		{UserID: alice.ID, PostID: reply.ID, CreatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	timeline, err := loadUserTimelinePageFromDB(owner.ID, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Items) != 1 || timeline.Items[0].Post.RepostCount != 2 {
		t.Fatalf("timeline items=%#v want root repost_count=2", timeline.Items)
	}

	var rootWithAuthor models.Post
	if err := preloadPostAuthor(publicPostScope(db.Model(&models.Post{}), time.Now().UTC())).First(&rootWithAuthor, root.ID).Error; err != nil {
		t.Fatal(err)
	}
	recommendations, err := selectedRecommendationResponses([]selectedRecommendation{{Post: rootWithAuthor}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendations) != 1 || recommendations[0].Post.RepostCount != 2 {
		t.Fatalf("recommendations=%#v want root repost_count=2", recommendations)
	}

	ctx, recorder := newReplyIntegrationContext(http.MethodGet, "/api/posts/"+strconvPostID(root.ID)+"/replies", strconvPostID(root.ID), "", 0)
	GetPostReplies(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("public replies status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var replies replyListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &replies); err != nil {
		t.Fatal(err)
	}
	if len(replies.Items) != 1 || replies.Items[0].RepostCount != 1 {
		t.Fatalf("replies=%#v want reply repost_count=1", replies.Items)
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
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Post{}, &models.PostMedia{}, &models.PostRepost{}); err != nil {
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
	if item == nil || item.ActivityType != timelineActivityRepost || item.Actor.ID != alice.ID || item.Post.ID != article.ID {
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
