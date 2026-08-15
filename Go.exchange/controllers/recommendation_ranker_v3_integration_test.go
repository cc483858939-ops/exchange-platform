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

func TestRulesV3FeedbackLoaderIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.RecommendationEvent{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	requestID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	article := models.Article{Model: gorm.Model{ID: uint(time.Now().UnixNano() & 0x3fffffff)}, AuthorID: author.ID, Title: "ranker v3", Category: "backend", Tags: []string{"go"}}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	events := []models.RecommendationEvent{
		{EventID: uuid.NewString(), UserID: userID, RequestID: requestID, ArticleID: article.ID, EventType: models.RecommendationEventTypeClick, Scene: recommendationScene, Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now, ReceivedAt: now, CreatedAt: now},
		{EventID: uuid.NewString(), UserID: userID, RequestID: uuid.NewString(), ArticleID: article.ID, EventType: models.RecommendationEventTypeImpression, Scene: recommendationScene, Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now, ReceivedAt: now, CreatedAt: now},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.RecommendationEvent{})
		db.Delete(&article)
		global.Db = originalDB
	})
	loaded, err := loadRecommendationFeedbackSignals(userID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].SignalType != "click" || loaded[0].Article == nil || loaded[0].Article.ID != article.ID {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestRulesV3FeedbackLimitsAndCandidateSuppressionIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.RecommendationEvent{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	completed := models.Article{Title: "completed", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now}}
	fallback := models.Article{Title: "fallback", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: "pending", Model: gorm.Model{CreatedAt: now}}
	if err := db.Create(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&fallback).Error; err != nil {
		t.Fatal(err)
	}
	events := make([]models.RecommendationEvent, 0, 507)
	for i := 0; i < 6; i++ {
		when := now.Add(-time.Duration(i) * time.Second)
		events = append(events, models.RecommendationEvent{EventID: uuid.NewString(), UserID: userID, RequestID: uuid.NewString(), ArticleID: completed.ID, EventType: models.RecommendationEventTypeClick, Scene: recommendationScene, Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID, OccurredAt: when, ReceivedAt: when, CreatedAt: when})
	}
	for i := 0; i < 500; i++ {
		when := now.Add(-time.Duration(i+10) * time.Second)
		events = append(events, models.RecommendationEvent{EventID: uuid.NewString(), UserID: userID, RequestID: uuid.NewString(), ArticleID: uint(900000 + i), EventType: models.RecommendationEventTypeClick, Scene: recommendationScene, Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID, OccurredAt: when, ReceivedAt: when, CreatedAt: when})
	}
	oldNegative := models.RecommendationEvent{EventID: uuid.NewString(), UserID: userID, RequestID: uuid.NewString(), ArticleID: fallback.ID, EventType: models.RecommendationEventTypeNotInterested, Scene: recommendationScene, Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab", StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now.Add(-time.Hour), ReceivedAt: now.Add(-time.Hour), CreatedAt: now}
	events = append(events, oldNegative)
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.RecommendationEvent{})
		db.Delete(&completed)
		db.Delete(&fallback)
		global.Db = originalDB
	})
	loaded, err := loadRecommendationFeedbackSignals(userID, now.AddDate(0, 0, -90))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != recommendationFeedbackArticleLimit {
		t.Fatalf("distinct article budget=%d", len(loaded))
	}
	clicks := 0
	for _, item := range loaded {
		if item.Event.ArticleID == completed.ID && item.SignalType == "click" {
			clicks++
		}
	}
	if clicks != 1 {
		t.Fatalf("duplicate article signals=%d", clicks)
	}
	candidates, err := loadRulesV3Candidates(userID, map[uint]struct{}{}, now.AddDate(0, 0, -90), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ID == fallback.ID {
			t.Fatal("not_interested outside profile 500 must still suppress fallback candidate")
		}
	}
}
