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

func openRulesV2IntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
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
	t.Cleanup(func() { global.Db = originalDB })
	return db
}

func newRulesV2IntegrationEvent(userID, articleID uint, eventType, eventID string, occurredAt, receivedAt time.Time) models.RecommendationEvent {
	return models.RecommendationEvent{
		EventID:          eventID,
		UserID:           userID,
		RequestID:        uuid.NewString(),
		ArticleID:        articleID,
		EventType:        eventType,
		Scene:            recommendationScene,
		Position:         1,
		RankerVersion:    recommendationRankerVersion,
		RankerConfigHash: "0123456789ab",
		StrategyID:       recommendationPersonalizedStrategyID,
		OccurredAt:       occurredAt,
		ReceivedAt:       receivedAt,
		CreatedAt:        receivedAt,
	}
}

func TestRulesV2FeedbackStableOrderIntegration(t *testing.T) {
	db := openRulesV2IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	events := []models.RecommendationEvent{
		newRulesV2IntegrationEvent(userID, 700001, models.RecommendationEventTypeClick, "00000000-0000-0000-0000-000000000003", now, now),
		newRulesV2IntegrationEvent(userID, 700002, models.RecommendationEventTypeClick, "00000000-0000-0000-0000-000000000001", now, now),
		newRulesV2IntegrationEvent(userID, 700003, models.RecommendationEventTypeClick, "00000000-0000-0000-0000-000000000002", now, now.Add(time.Microsecond)),
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.RecommendationEvent{})
	})

	loaded, err := loadRecommendationFeedbackSignals(userID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000001",
	}
	if len(loaded) != len(want) {
		t.Fatalf("loaded=%d want=%d", len(loaded), len(want))
	}
	for i, eventID := range want {
		if loaded[i].Event.EventID != eventID {
			t.Fatalf("order[%d]=%s want=%s", i, loaded[i].Event.EventID, eventID)
		}
	}
}

func TestRulesV2CandidateNegativeSuppressionWindowIntegration(t *testing.T) {
	db := openRulesV2IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lookbackStart := now.AddDate(0, 0, -90)
	author := createArticleIntegrationAuthor(t, db)

	completedSuppressed := models.Article{Title: "completed suppressed", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now}}
	fallbackSuppressed := models.Article{Title: "fallback suppressed", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: "pending", Model: gorm.Model{CreatedAt: now}}
	expiredNegativeAllowed := models.Article{Title: "expired negative allowed", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: "pending", Model: gorm.Model{CreatedAt: now}}
	completedAllowed := models.Article{Title: "completed allowed", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now}}
	articles := []*models.Article{&completedSuppressed, &fallbackSuppressed, &expiredNegativeAllowed, &completedAllowed}
	for _, article := range articles {
		if err := db.Create(article).Error; err != nil {
			t.Fatal(err)
		}
	}

	events := []models.RecommendationEvent{
		newRulesV2IntegrationEvent(userID, completedSuppressed.ID, models.RecommendationEventTypeNotInterested, uuid.NewString(), now.Add(-time.Hour), now.Add(-time.Hour)),
		newRulesV2IntegrationEvent(userID, fallbackSuppressed.ID, models.RecommendationEventTypeNotInterested, uuid.NewString(), now.Add(-time.Hour), now.Add(-time.Hour)),
		newRulesV2IntegrationEvent(userID, expiredNegativeAllowed.ID, models.RecommendationEventTypeNotInterested, uuid.NewString(), now.AddDate(0, 0, -91), now.AddDate(0, 0, -91)),
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&models.RecommendationEvent{})
		ids := []uint{completedSuppressed.ID, fallbackSuppressed.ID, expiredNegativeAllowed.ID, completedAllowed.ID}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	candidates, err := loadRulesV2Candidates(userID, map[uint]struct{}{}, lookbackStart, now)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		got[candidate.ID] = struct{}{}
	}
	if _, ok := got[completedSuppressed.ID]; ok {
		t.Fatal("current not_interested must suppress completed candidate")
	}
	if _, ok := got[fallbackSuppressed.ID]; ok {
		t.Fatal("current not_interested must suppress fallback candidate")
	}
	if _, ok := got[expiredNegativeAllowed.ID]; !ok {
		t.Fatal("not_interested older than lookback must not suppress candidate")
	}
	if _, ok := got[completedAllowed.ID]; !ok {
		t.Fatal("completed candidate without negative feedback must remain eligible")
	}
}

func TestRulesV2CandidateStableOrderIntegration(t *testing.T) {
	db := openRulesV2IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)

	author := createArticleIntegrationAuthor(t, db)
	completedLowID := models.Article{Title: "completed low id", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now}}
	completedHighID := models.Article{Title: "completed high id", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: consts.ArticleAnalysisStateCompleted, Model: gorm.Model{CreatedAt: now}}
	fallbackLowID := models.Article{Title: "fallback low id", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: "pending", LikeCount: 7, Model: gorm.Model{CreatedAt: now}}
	fallbackHighID := models.Article{Title: "fallback high id", AuthorID: author.ID, PublicationState: consts.ArticlePublicationStatePublished, AnalysisState: "pending", LikeCount: 7, Model: gorm.Model{CreatedAt: now}}
	articles := []*models.Article{&completedLowID, &completedHighID, &fallbackLowID, &fallbackHighID}
	for _, article := range articles {
		if err := db.Create(article).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ids := []uint{completedLowID.ID, completedHighID.ID, fallbackLowID.ID, fallbackHighID.ID}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	candidates, err := loadRulesV2Candidates(userID, map[uint]struct{}{}, now.AddDate(0, 0, -90), now)
	if err != nil {
		t.Fatal(err)
	}
	positions := make(map[uint]int, len(candidates))
	for position, candidate := range candidates {
		positions[candidate.ID] = position
	}
	for _, article := range articles {
		if _, ok := positions[article.ID]; !ok {
			t.Fatalf("candidate %d is missing from result", article.ID)
		}
	}
	if positions[completedHighID.ID] >= positions[completedLowID.ID] {
		t.Fatalf("completed ID tiebreak order=%v", positions)
	}
	if positions[fallbackHighID.ID] >= positions[fallbackLowID.ID] {
		t.Fatalf("fallback ID tiebreak order=%v", positions)
	}
	if positions[completedLowID.ID] >= positions[fallbackHighID.ID] {
		t.Fatalf("completed candidates must precede fallback candidates: order=%v", positions)
	}
}
func TestRulesV2CandidateLoadsCommentCountIntegration(t *testing.T) {
	db := openRulesV2IntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	article := models.Article{
		Title:            "comment count candidate",
		AuthorID:         author.ID,
		PublicationState: consts.ArticlePublicationStatePublished,
		AnalysisState:    consts.ArticleAnalysisStateCompleted,
		LikeCount:        17,
		CommentCount:     8,
		Model:            gorm.Model{CreatedAt: now},
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&article)
	})

	candidates, err := loadRulesV2Candidates(uint(now.UnixNano()&0x3fffffff), map[uint]struct{}{}, now.AddDate(0, 0, -90), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.ID == article.ID {
			if candidate.LikeCount != 17 || candidate.CommentCount != 8 {
				t.Fatalf("candidate like_count=%d comment_count=%d", candidate.LikeCount, candidate.CommentCount)
			}
			return
		}
	}
	t.Fatalf("candidate %d is missing", article.ID)
}
