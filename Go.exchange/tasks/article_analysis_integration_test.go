package tasks

import (
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestFinishArticleAnalysisSuccessInvalidatesDetailCacheAfterCommitIntegration(t *testing.T) {
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

	originalDB := global.Db
	originalInvalidator := invalidateArticleDetailCache
	global.Db = db
	var invalidationCalls int
	invalidateArticleDetailCache = func(articleID uint) error {
		invalidationCalls++
		var article models.Article
		if err := db.First(&article, articleID).Error; err != nil {
			return err
		}
		if article.Summary != "committed summary" || article.Category != "News" || article.AnalysisState != "completed" {
			t.Errorf("cache invalidated before committed article update: %#v", article)
		}
		var current models.ArticleAnalysisJob
		if err := db.Where("article_id = ?", articleID).First(&current).Error; err != nil {
			return err
		}
		if current.State != models.ArticleAnalysisJobSucceeded {
			t.Errorf("cache invalidated before committed job update: %#v", current)
		}
		return nil
	}
	t.Cleanup(func() {
		invalidateArticleDetailCache = originalInvalidator
		global.Db = originalDB
	})

	author := models.User{Username: "article-analysis-cache-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Article{AuthorID: author.ID, Title: "analysis", Content: "content", Preview: "preview"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := models.ArticleAnalysisJob{
		ArticleID:     article.ID,
		State:         models.ArticleAnalysisJobLeased,
		AttemptCount:  1,
		MaxAttempts:   5,
		NextAttemptAt: now,
		LeasedBy:      "integration-test",
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&models.ArticleAnalysisJob{}, job.ID)
		db.Unscoped().Delete(&models.Article{}, article.ID)
		db.Unscoped().Delete(&models.User{}, author.ID)
	})

	if err := finishArticleAnalysisSuccess(job, ArticleAnalysisResult{Summary: "committed summary", Tags: []string{"go"}, Category: "News"}, now); err != nil {
		t.Fatal(err)
	}
	if invalidationCalls != 1 {
		t.Fatalf("successful analysis invalidations=%d want 1", invalidationCalls)
	}

	if err := db.Model(&models.ArticleAnalysisJob{}).Where("id = ?", job.ID).Update("state", models.ArticleAnalysisJobQueued).Error; err != nil {
		t.Fatal(err)
	}
	if err := finishArticleAnalysisSuccess(job, ArticleAnalysisResult{Summary: "must not commit", Category: "Other"}, now.Add(time.Second)); err == nil {
		t.Fatal("expected non-leased job to fail")
	}
	if invalidationCalls != 1 {
		t.Fatalf("failed analysis invalidations=%d want 1", invalidationCalls)
	}
}
