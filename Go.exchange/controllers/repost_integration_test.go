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

func TestArticleRepostIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleRepost{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uidx_article_reposts_user_article ON article_reposts (user_id, article_id)").Error; err != nil {
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
		db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.ArticleRepost{})
		db.Unscoped().Where("author_id IN ?", userIDs).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	publishedAt := time.Now().UTC().Add(-time.Minute)
	article := models.Article{
		AuthorID:         owner.ID,
		Title:            "Repost integration article",
		Content:          "Repost integration body",
		Preview:          "Repost integration preview",
		PublicationState: "published",
		PublishedAt:      &publishedAt,
		Model:            gorm.Model{CreatedAt: publishedAt},
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
			ctx, recorder := newRepostTestContext(http.MethodPut, "/api/articles/"+strconvArticleID(article.ID)+"/repost", "", &viewer.ID)
			RepostArticle(ctx)
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

	state, err := loadArticleRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 1 || !state.Reposted {
		t.Fatalf("concurrent same-viewer state=%#v", state)
	}

	ctx, recorder := newRepostTestContext(http.MethodPut, "/api/articles/"+strconvArticleID(article.ID)+"/repost", "", &otherViewer.ID)
	RepostArticle(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second viewer PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state, err = loadArticleRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 2 || !state.Reposted {
		t.Fatalf("viewer state after second viewer=%#v", state)
	}
	state, err = loadArticleRepostStateWithDB(db, owner.ID, article.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if state.Reposts != 2 || state.Reposted {
		t.Fatalf("viewer isolation state=%#v", state)
	}

	ctx, recorder = newRepostTestContext(http.MethodPut, "/api/articles/"+strconvArticleID(article.ID)+"/repost", "", &viewer.ID)
	RepostArticle(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("duplicate PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state, err = loadArticleRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil || state.Reposts != 2 || !state.Reposted {
		t.Fatalf("duplicate PUT changed state=%#v err=%v", state, err)
	}

	for index := 0; index < 2; index++ {
		ctx, recorder = newRepostTestContext(http.MethodDelete, "/api/articles/"+strconvArticleID(article.ID)+"/repost", "", &viewer.ID)
		UndoRepostArticle(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("DELETE %d status=%d body=%s", index+1, recorder.Code, recorder.Body.String())
		}
	}
	state, err = loadArticleRepostStateWithDB(db, viewer.ID, article.ID, time.Now().UTC())
	if err != nil || state.Reposts != 1 || state.Reposted {
		t.Fatalf("idempotent DELETE state=%#v err=%v", state, err)
	}

	expired := time.Now().UTC().Add(-time.Hour)
	article.ExpiredAt = &expired
	if err := db.Save(&article).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder = newRepostTestContext(http.MethodPut, "/api/articles/"+strconvArticleID(article.ID)+"/repost", "", &viewer.ID)
	RepostArticle(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unavailable PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
