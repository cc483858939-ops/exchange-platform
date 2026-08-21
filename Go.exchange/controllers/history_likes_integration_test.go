package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openLikedHistoryIntegrationDatabase(t *testing.T) *gorm.DB {
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

func requestLikedHistory(t *testing.T, viewerID uint, query string) (articlePageResponse, int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/api/me/history/likes"
	if query != "" {
		path += "?" + query
	}
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("user_id", viewerID)
	GetMyLikedHistory(ctx)
	var response articlePageResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, recorder.Code, recorder.Body.String()
}

func likedHistoryArticleIDs(items []articleResponse) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestLikedHistoryIntegration(t *testing.T) {
	db := openLikedHistoryIntegrationDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleReaction{}); err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	users := []models.User{
		{Username: "liked-history-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "liked-history-other-viewer-" + uuid.NewString(), Password: "secret"},
		{Username: "liked-history-author-" + uuid.NewString(), Password: "secret", DisplayName: "History Author", AvatarURL: "author.jpg"},
		{Username: "liked-history-soft-author-" + uuid.NewString(), Password: "secret"},
		{Username: "liked-history-empty-viewer-" + uuid.NewString(), Password: "secret"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	viewer, otherViewer, author, softAuthor, emptyViewer := users[0], users[1], users[2], users[3], users[4]
	userIDs := []uint{viewer.ID, otherViewer.ID, author.ID, softAuthor.ID, emptyViewer.ID}

	var articleIDs []uint
	t.Cleanup(func() {
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.ArticleReaction{})
		if len(articleIDs) > 0 {
			db.Unscoped().Where("id IN ?", articleIDs).Delete(&models.Article{})
		}
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	baseTime := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	createArticle := func(authorID uint, title string, createdAt time.Time, publicationState string, publishedAt *time.Time, expiredAt *time.Time) models.Article {
		article := models.Article{
			AuthorID:         authorID,
			Title:            title,
			Content:          title + " body",
			Preview:          "preview",
			PublicationState: publicationState,
			PublishedAt:      publishedAt,
			ExpiredAt:        expiredAt,
			Model:            gorm.Model{CreatedAt: createdAt},
		}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		articleIDs = append(articleIDs, article.ID)
		return article
	}
	createPublished := func(authorID uint, title string, createdAt time.Time) models.Article {
		publishedAt := createdAt
		return createArticle(authorID, title, createdAt, consts.ArticlePublicationStatePublished, &publishedAt, nil)
	}

	newReactionOldArticle := createPublished(author.ID, "reaction newer, article older", baseTime.Add(1*time.Hour))
	tieLowerIDArticle := createPublished(author.ID, "tie lower id", baseTime.Add(10*time.Hour))
	tieHigherIDArticle := createPublished(author.ID, "tie higher id", baseTime.Add(11*time.Hour))
	oldReactionNewArticle := createPublished(author.ID, "reaction older, article newer", baseTime.Add(12*time.Hour))
	unlikedArticle := createPublished(author.ID, "currently unliked", baseTime.Add(13*time.Hour))
	otherViewerArticle := createPublished(author.ID, "other viewer only", baseTime.Add(14*time.Hour))

	draftArticle := createPublished(author.ID, "draft", baseTime.Add(15*time.Hour))
	if err := db.Model(&draftArticle).Update("publication_state", "draft").Error; err != nil {
		t.Fatal(err)
	}
	futurePublishedAt := time.Now().UTC().Add(24 * time.Hour)
	futureArticle := createArticle(author.ID, "future", baseTime.Add(16*time.Hour), consts.ArticlePublicationStatePublished, &futurePublishedAt, nil)
	expiredAt := time.Now().UTC().Add(-time.Hour)
	expiredArticle := createArticle(author.ID, "expired", baseTime.Add(17*time.Hour), consts.ArticlePublicationStatePublished, func() *time.Time {
		publishedAt := baseTime.Add(17 * time.Hour)
		return &publishedAt
	}(), &expiredAt)
	deletedArticle := createPublished(author.ID, "deleted", baseTime.Add(18*time.Hour))
	if err := db.Delete(&deletedArticle).Error; err != nil {
		t.Fatal(err)
	}
	softAuthorArticle := createPublished(softAuthor.ID, "soft deleted author", baseTime.Add(19*time.Hour))
	if err := db.Delete(&softAuthor).Error; err != nil {
		t.Fatal(err)
	}

	createReaction := func(articleID, userID uint, liked bool, stateChangedAt time.Time, version int64) models.ArticleReaction {
		reaction := models.ArticleReaction{
			UserID:         userID,
			ArticleID:      articleID,
			Reaction:       models.ArticleReactionLike,
			Liked:          liked,
			Version:        version,
			StateChangedAt: stateChangedAt,
		}
		if err := db.Create(&reaction).Error; err != nil {
			t.Fatal(err)
		}
		return reaction
	}

	createReaction(newReactionOldArticle.ID, viewer.ID, true, baseTime.Add(5*time.Hour), 1)
	createReaction(tieLowerIDArticle.ID, viewer.ID, true, baseTime.Add(4*time.Hour), 1)
	createReaction(tieHigherIDArticle.ID, viewer.ID, true, baseTime.Add(4*time.Hour), 1)
	createReaction(oldReactionNewArticle.ID, viewer.ID, true, baseTime.Add(3*time.Hour), 1)
	createReaction(unlikedArticle.ID, viewer.ID, false, baseTime.Add(6*time.Hour), 1)
	createReaction(otherViewerArticle.ID, otherViewer.ID, true, baseTime.Add(9*time.Hour), 1)
	createReaction(draftArticle.ID, viewer.ID, true, baseTime.Add(10*time.Hour), 1)
	createReaction(futureArticle.ID, viewer.ID, true, baseTime.Add(11*time.Hour), 1)
	createReaction(expiredArticle.ID, viewer.ID, true, baseTime.Add(12*time.Hour), 1)
	createReaction(deletedArticle.ID, viewer.ID, true, baseTime.Add(13*time.Hour), 1)
	createReaction(softAuthorArticle.ID, viewer.ID, true, baseTime.Add(14*time.Hour), 1)

	queryLogger := &articleDetailSQLLogger{Interface: logger.Default}
	global.Db = db.Session(&gorm.Session{Logger: queryLogger})
	page1, status, body := requestLikedHistory(t, viewer.ID, "limit=2")
	if status != http.StatusOK || len(page1.Items) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1 status=%d body=%s response=%#v", status, body, page1)
	}
	if got := likedHistoryArticleIDs(page1.Items); len(got) != 2 || got[0] != newReactionOldArticle.ID || got[1] != tieLowerIDArticle.ID {
		t.Fatalf("page1 order=%v", got)
	}
	if page1.Items[0].Title != "reaction newer, article older" || page1.Items[0].Author.ID != author.ID || page1.Items[0].Author.DisplayName != "History Author" || page1.Items[0].Author.AvatarURL != "author.jpg" {
		t.Fatalf("canonical article response=%#v", page1.Items[0])
	}

	cursor, err := decodeLikedHistoryCursor(*page1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.StateChangedAt.IsZero() || cursor.ArticleID != tieLowerIDArticle.ID {
		t.Fatalf("reaction metadata cursor=%#v", cursor)
	}

	page2, status, body := requestLikedHistory(t, viewer.ID, "limit=2&cursor="+*page1.NextCursor)
	if status != http.StatusOK || len(page2.Items) != 2 || page2.NextCursor != nil {
		t.Fatalf("page2 status=%d body=%s response=%#v", status, body, page2)
	}
	if got := likedHistoryArticleIDs(page2.Items); len(got) != 2 || got[0] != tieHigherIDArticle.ID || got[1] != oldReactionNewArticle.ID {
		t.Fatalf("page2 order=%v", got)
	}
	allIDs := append(likedHistoryArticleIDs(page1.Items), likedHistoryArticleIDs(page2.Items)...)
	expectedIDs := []uint{newReactionOldArticle.ID, tieLowerIDArticle.ID, tieHigherIDArticle.ID, oldReactionNewArticle.ID}
	if len(allIDs) != len(expectedIDs) {
		t.Fatalf("all ids=%v expected=%v", allIDs, expectedIDs)
	}
	for index, id := range expectedIDs {
		if allIDs[index] != id {
			t.Fatalf("all ids=%v expected=%v", allIDs, expectedIDs)
		}
	}
	for _, excluded := range []uint{unlikedArticle.ID, otherViewerArticle.ID, draftArticle.ID, futureArticle.ID, expiredArticle.ID, deletedArticle.ID, softAuthorArticle.ID} {
		if containsArticleID(page1.Items, excluded) || containsArticleID(page2.Items, excluded) {
			t.Fatalf("excluded article %d appeared", excluded)
		}
	}

	empty, status, body := requestLikedHistory(t, emptyViewer.ID, "")
	if status != http.StatusOK || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty status=%d body=%s response=%#v", status, body, empty)
	}

	relikedAt := baseTime.Add(20 * time.Hour)
	if err := db.Model(&models.ArticleReaction{}).
		Where("user_id = ? AND article_id = ?", viewer.ID, oldReactionNewArticle.ID).
		Updates(map[string]interface{}{"liked": true, "state_changed_at": relikedAt, "reaction_version": int64(2)}).Error; err != nil {
		t.Fatal(err)
	}
	reliked, status, body := requestLikedHistory(t, viewer.ID, "limit=1")
	if status != http.StatusOK || len(reliked.Items) != 1 || reliked.Items[0].ID != oldReactionNewArticle.ID {
		t.Fatalf("re-like status=%d body=%s response=%#v", status, body, reliked)
	}

	queries := queryLogger.snapshot()
	membershipQueries := 0
	for _, query := range queries {
		normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
		if strings.Contains(normalized, "from article_reaction") {
			membershipQueries++
		}
	}
	if membershipQueries < 3 {
		t.Fatalf("expected one bounded reaction membership query per request, got %d queries=%v", membershipQueries, queries)
	}
	if len(queries) > 30 {
		t.Fatalf("liked history used too many queries, possible N+1: %d queries=%v", len(queries), queries)
	}

	if err := db.Delete(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	_, status, body = requestLikedHistory(t, viewer.ID, "")
	if status != http.StatusUnauthorized || body == "" {
		t.Fatalf("soft-deleted viewer status=%d body=%s", status, body)
	}

}
