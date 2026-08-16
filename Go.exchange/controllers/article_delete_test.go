package controllers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newArticleDeleteContext(id string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/articles/"+id, nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func stubArticleDeleteDependencies(t *testing.T, transactionErr error) {
	t.Helper()
	originalViewer := loadArticleDeleteViewer
	originalTransaction := deleteArticleInTransaction
	originalDetail := invalidateArticleDeleteDetailCache
	originalLikes := cleanupDeletedArticleLikeState
	t.Cleanup(func() {
		loadArticleDeleteViewer = originalViewer
		deleteArticleInTransaction = originalTransaction
		invalidateArticleDeleteDetailCache = originalDetail
		cleanupDeletedArticleLikeState = originalLikes
	})

	loadArticleDeleteViewer = func(uint) error { return nil }
	deleteArticleInTransaction = func(uint, uint) error { return transactionErr }
	invalidateArticleDeleteDetailCache = func(uint) error { return nil }
	cleanupDeletedArticleLikeState = func(uint) error { return nil }
}

func TestDeleteArticleRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc"} {
		ctx, recorder := newArticleDeleteContext(id, nil)
		DeleteArticle(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("id=%q status=%d body=%s", id, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDeleteArticleRejectsMissingAuthContext(t *testing.T) {
	ctx, recorder := newArticleDeleteContext("42", nil)
	DeleteArticle(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeleteArticleRejectsMissingOrInactiveViewer(t *testing.T) {
	originalViewer := loadArticleDeleteViewer
	t.Cleanup(func() { loadArticleDeleteViewer = originalViewer })
	loadArticleDeleteViewer = func(uint) error { return gorm.ErrRecordNotFound }

	viewerID := uint(7)
	ctx, recorder := newArticleDeleteContext("42", &viewerID)
	DeleteArticle(ctx)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeleteArticleMapsMissingAndForbiddenTransactions(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing", err: errArticleDeleteNotFound, want: http.StatusNotFound},
		{name: "forbidden", err: errArticleDeleteForbidden, want: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubArticleDeleteDependencies(t, testCase.err)
			ctx, recorder := newArticleDeleteContext("42", &viewerID)
			DeleteArticle(ctx)
			if recorder.Code != testCase.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if testCase.want == http.StatusNotFound && recorder.Body.String() != "{\"error\":\"article not found\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
			if testCase.want == http.StatusForbidden && recorder.Body.String() != "{\"error\":\"forbidden\"}" {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
}

func TestDeleteArticleOwnerReturnsNoContentAndCleansUpOnce(t *testing.T) {
	viewerID := uint(7)
	var detailCalls, likeCalls int
	stubArticleDeleteDependencies(t, nil)
	invalidateArticleDeleteDetailCache = func(uint) error {
		detailCalls++
		return errors.New("detail cache unavailable")
	}
	cleanupDeletedArticleLikeState = func(uint) error {
		likeCalls++
		return errors.New("redis unavailable")
	}

	ctx, recorder := newArticleDeleteContext("42", &viewerID)
	DeleteArticle(ctx)
	ctx.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if detailCalls != 1 || likeCalls != 1 {
		t.Fatalf("cleanup calls detail=%d likes=%d", detailCalls, likeCalls)
	}
}

func TestDeleteArticleDoesNotCleanUpRejectedRequest(t *testing.T) {
	viewerID := uint(7)
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: errArticleDeleteNotFound},
		{name: "forbidden", err: errArticleDeleteForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stubArticleDeleteDependencies(t, testCase.err)
			var calls int

			invalidateArticleDeleteDetailCache = func(uint) error { calls++; return nil }
			cleanupDeletedArticleLikeState = func(uint) error { calls++; return nil }
			ctx, _ := newArticleDeleteContext("42", &viewerID)
			DeleteArticle(ctx)
			if calls != 0 {
				t.Fatalf("cleanup calls=%d", calls)
			}
		})
	}
}

func openArticleDeleteIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleAnalysisJob{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDeleteArticleIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openArticleDeleteIntegrationDatabase(t)
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })

	owner := models.User{Username: "delete-owner-" + uuid.NewString(), Password: "test"}
	other := models.User{Username: "delete-other-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id IN (SELECT id FROM articles WHERE author_id IN (?, ?))", owner.ID, other.ID).Delete(&models.ArticleAnalysisJob{})
		db.Unscoped().Where("author_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", []uint{owner.ID, other.ID}).Delete(&models.User{})
	})

	deleteWithJob := func(state string) (models.Article, models.ArticleAnalysisJob) {
		article := models.Article{
			AuthorID: owner.ID,
			Title:    "delete fixture",
			Content:  "delete fixture body",
			Preview:  "delete fixture",
		}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		job := models.ArticleAnalysisJob{
			ArticleID:     article.ID,
			State:         state,
			NextAttemptAt: time.Now().UTC(),
			LeasedBy:      "worker",
		}
		if state == models.ArticleAnalysisJobLeased {
			leaseUntil := time.Now().UTC().Add(time.Hour)
			job.LeaseUntil = &leaseUntil
		}
		if err := db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
		return article, job
	}

	forbiddenArticle := models.Article{
		AuthorID: owner.ID,
		Title:    "forbidden fixture",
		Content:  "forbidden body",
		Preview:  "forbidden",
	}
	if err := db.Create(&forbiddenArticle).Error; err != nil {
		t.Fatal(err)
	}
	ctx, recorder := newArticleDeleteContext(strconvArticleID(forbiddenArticle.ID), &other.ID)
	DeleteArticle(ctx)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	queuedArticle, queuedJob := deleteWithJob(models.ArticleAnalysisJobQueued)
	retryArticle, retryJob := deleteWithJob(models.ArticleAnalysisJobRetryWait)
	leasedArticle, leasedJob := deleteWithJob(models.ArticleAnalysisJobLeased)
	succeededArticle, succeededJob := deleteWithJob(models.ArticleAnalysisJobSucceeded)
	deadArticle, deadJob := deleteWithJob(models.ArticleAnalysisJobDead)
	noJobArticle := models.Article{AuthorID: owner.ID, Title: "no job", Content: "no job body", Preview: "no job"}
	if err := db.Create(&noJobArticle).Error; err != nil {
		t.Fatal(err)
	}

	deleteOne := func(articleID, viewerID uint) int {
		ctx, recorder := newArticleDeleteContext(strconvArticleID(articleID), &viewerID)
		DeleteArticle(ctx)
		ctx.Writer.WriteHeaderNow()
		return recorder.Code
	}
	for _, article := range []models.Article{queuedArticle, retryArticle, leasedArticle, succeededArticle, deadArticle, noJobArticle} {
		if status := deleteOne(article.ID, owner.ID); status != http.StatusNoContent {
			t.Fatalf("article=%d status=%d", article.ID, status)
		}
	}
	if status := deleteOne(queuedArticle.ID, owner.ID); status != http.StatusNotFound {
		t.Fatalf("repeat delete status=%d", status)
	}

	var canceledQueued, canceledRetry models.ArticleAnalysisJob
	if err := db.First(&canceledQueued, queuedJob.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&canceledRetry, retryJob.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, job := range []models.ArticleAnalysisJob{canceledQueued, canceledRetry} {
		if job.State != models.ArticleAnalysisJobCanceled || job.LeaseUntil != nil || job.LeasedBy != "" || job.FinishedAt == nil || job.LastError != "article deleted" {
			t.Fatalf("canceled job=%#v", job)
		}
	}

	for _, fixture := range []struct {
		name string
		id   uint
		job  models.ArticleAnalysisJob
	}{
		{name: "leased", id: leasedArticle.ID, job: leasedJob},
		{name: "succeeded", id: succeededArticle.ID, job: succeededJob},
		{name: "dead", id: deadArticle.ID, job: deadJob},
	} {
		var got models.ArticleAnalysisJob
		if err := db.First(&got, fixture.job.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.State != fixture.job.State || got.LeasedBy != fixture.job.LeasedBy {
			t.Fatalf("%s job changed before=%#v after=%#v", fixture.name, fixture.job, got)
		}
		var visible models.Article
		if err := db.First(&visible, fixture.id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("%s visible article err=%v", fixture.name, err)
		}
	}

	var deleted models.Article
	if err := db.Unscoped().First(&deleted, queuedArticle.ID).Error; err != nil || !deleted.DeletedAt.Valid {
		t.Fatalf("soft deleted article=%#v err=%v", deleted, err)
	}

	raceArticle := models.Article{AuthorID: owner.ID, Title: "race", Content: "race body", Preview: "race"}
	if err := db.Create(&raceArticle).Error; err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			statuses <- deleteOne(raceArticle.ID, owner.ID)
		}()
	}
	waitGroup.Wait()
	close(statuses)
	successes, notFound := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			successes++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("race status=%d", status)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("race successes=%d notFound=%d", successes, notFound)
	}
}

func strconvArticleID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
