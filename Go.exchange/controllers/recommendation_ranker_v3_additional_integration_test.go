package controllers

import (
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openRulesV3IntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ArticleBehavior{}, &models.ArticleReaction{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })
	return db
}

func TestRulesV3FeedbackStableOrderUsesProjectionTimestampsIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	behaviors := []models.ArticleBehavior{
		{UserID: userID, ArticleID: 700001, Action: eventing.RecommendationBehaviorActionClick, Count: 1, LastSeenAt: now, Active: true},
		{UserID: userID, ArticleID: 700002, Action: eventing.RecommendationBehaviorActionClick, Count: 1, LastSeenAt: now, Active: true},
		{UserID: userID, ArticleID: 700003, Action: eventing.RecommendationBehaviorActionClick, Count: 1, LastSeenAt: now, Active: true},
	}
	if err := db.Create(&behaviors).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.ArticleBehavior{})
	})

	loaded, err := loadRecommendationFeedbackSignals(userID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]models.ArticleBehavior(nil), behaviors...)
	sort.Slice(expected, func(i, j int) bool {
		return expected[i].ID > expected[j].ID
	})
	if len(loaded) != len(expected) {
		t.Fatalf("loaded=%d want=%d", len(loaded), len(expected))
	}
	for index, behavior := range expected {
		want := strconv.FormatUint(uint64(behavior.ID), 10)
		if loaded[index].Event.EventID != want {
			t.Fatalf("order[%d]=%s want=%s generated_id=%d", index, loaded[index].Event.EventID, want, behavior.ID)
		}
	}
}

func TestRulesV3CandidateNegativeSuppressionWindowUsesProjectionIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lookbackStart := now.AddDate(0, 0, -90)
	author := createArticleIntegrationAuthor(t, db)
	suppressed := models.Article{
		AuthorID: author.ID, Title: "suppressed",
		PublicationState: consts.ArticlePublicationStatePublished, PublishedAt: &now,
		AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now},
	}
	expiredNegativeAllowed := models.Article{
		AuthorID: author.ID, Title: "expired negative allowed",
		PublicationState: consts.ArticlePublicationStatePublished, PublishedAt: &now,
		AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now},
	}
	if err := db.Create(&[]*models.Article{&suppressed, &expiredNegativeAllowed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]models.ArticleBehavior{
		{UserID: userID, ArticleID: suppressed.ID, Action: eventing.RecommendationBehaviorActionNotInterested, Count: 1, LastSeenAt: now.Add(-time.Hour), Active: true},
		{UserID: userID, ArticleID: expiredNegativeAllowed.ID, Action: eventing.RecommendationBehaviorActionNotInterested, Count: 1, LastSeenAt: now.AddDate(0, 0, -91), Active: true},
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.ArticleBehavior{})
		db.Unscoped().Where("id IN ?", []uint{suppressed.ID, expiredNegativeAllowed.ID}).Delete(&models.Article{})
	})

	candidates, err := loadRulesV3Candidates(userID, userInterestProfile{}, lookbackStart, now, defaultRecommendationLimit)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate.ID] = struct{}{}
	}
	if _, ok := seen[suppressed.ID]; ok {
		t.Fatal("current derived not_interested must suppress candidate")
	}
	if _, ok := seen[expiredNegativeAllowed.ID]; !ok {
		t.Fatal("expired derived not_interested must not suppress candidate")
	}
}

func TestRulesV3CandidateScopeKeepsLaterReactionOverrideIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	article := models.Article{
		AuthorID: author.ID, Title: "reaction override",
		PublicationState: consts.ArticlePublicationStatePublished, PublishedAt: &now,
		AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now},
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	negativeAt := now.Add(-time.Hour)
	if err := db.Create(&models.ArticleBehavior{
		UserID: userID, ArticleID: article.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested,
		Count:  1, LastSeenAt: negativeAt, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleReaction{
		UserID: userID, ArticleID: article.ID, Reaction: 0, Liked: false,
		StateChangedAt: now.Add(-30 * time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ? AND article_id = ?", userID, article.ID).Delete(&models.ArticleBehavior{})
		db.Where("user_id = ? AND article_id = ?", userID, article.ID).Delete(&models.ArticleReaction{})
		db.Unscoped().Delete(&article)
	})

	candidates, err := loadRulesV3Candidates(userID, userInterestProfile{}, now.AddDate(0, 0, -90), now, defaultRecommendationLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ID == article.ID {
			return
		}
	}
	t.Fatal("later reaction must override derived not_interested suppression")
}
