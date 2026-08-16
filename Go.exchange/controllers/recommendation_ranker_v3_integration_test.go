package controllers

import (
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

func TestRulesV3FeedbackLoaderUsesDerivedBehaviorProjectionIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	article := models.Article{
		AuthorID: author.ID, Title: "ranker v3 derived behavior",
		Category: "backend", Tags: []string{"go"},
		PublicationState: consts.ArticlePublicationStatePublished,
		PublishedAt:      &now, AnalysisState: consts.ArticleAnalysisStateCompleted,
		Model: gorm.Model{CreatedAt: now},
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleBehavior{
		UserID: userID, ArticleID: article.ID,
		Action: eventing.RecommendationBehaviorActionClick,
		Count:  2, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.ArticleBehavior{})
		db.Unscoped().Delete(&article)
		global.Db = nil
	})

	loaded, err := loadRecommendationFeedbackSignals(userID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].SignalType != "click" ||
		loaded[0].Article == nil || loaded[0].Article.ID != article.ID {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestRulesV3DerivedNegativeBehaviorSuppressesCandidatesIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	suppressed := models.Article{
		AuthorID: author.ID, Title: "suppressed", PublicationState: consts.ArticlePublicationStatePublished,
		PublishedAt: &now, AnalysisState: consts.ArticleAnalysisStateCompleted,
		Model: gorm.Model{CreatedAt: now},
	}
	allowed := models.Article{
		AuthorID: author.ID, Title: "allowed", PublicationState: consts.ArticlePublicationStatePublished,
		PublishedAt: &now, AnalysisState: consts.ArticleAnalysisStateCompleted,
		Model: gorm.Model{CreatedAt: now},
	}
	if err := db.Create(&[]*models.Article{&suppressed, &allowed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleBehavior{
		UserID: userID, ArticleID: suppressed.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested,
		Count:  1, LastSeenAt: now.Add(-time.Hour), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("id IN ?", []uint{suppressed.ID, allowed.ID}).Delete(&models.Article{})
		global.Db = nil
	})

	candidates, err := loadRulesV3Candidates(userID, userInterestProfile{}, now.AddDate(0, 0, -90), now, defaultRecommendationLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ID == suppressed.ID {
			t.Fatal("derived not_interested behavior must suppress candidate")
		}
	}
}
