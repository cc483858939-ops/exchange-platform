package controllers

import (
	"os"
	"strconv"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPersistRecommendationEventsCreatesOneOutboxAndDeduplicatesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.RecommendationEvent{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	requestID := uuid.NewString()
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	userAggregateID := strconv.FormatUint(uint64(userID), 10)
	t.Cleanup(func() {
		db.Where("request_id = ?", requestID).Delete(&models.RecommendationEvent{})
		db.Where("event_type = ? AND aggregate_id = ?", eventing.EventTypeRecommendationEventsRecorded, userAggregateID).Delete(&models.OutboxEvent{})
		global.Db = originalDB
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	events := []models.RecommendationEvent{
		{
			EventID: uuid.NewString(), UserID: userID, RequestID: requestID, ArticleID: 11,
			EventType: models.RecommendationEventTypeImpression, Scene: recommendationScene,
			Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab",
			StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now, ReceivedAt: now, CreatedAt: now,
		},
		{
			EventID: uuid.NewString(), UserID: userID, RequestID: requestID, ArticleID: 11,
			EventType: models.RecommendationEventTypeClick, Scene: recommendationScene,
			Position: 1, RankerVersion: recommendationRankerVersion, RankerConfigHash: "0123456789ab",
			StrategyID: recommendationPersonalizedStrategyID, OccurredAt: now, ReceivedAt: now, CreatedAt: now,
		},
	}
	first, err := persistRecommendationEvents(userID, events)
	if err != nil {
		t.Fatal(err)
	}
	second, err := persistRecommendationEvents(userID, events)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Status != "accepted" || first[1].Status != "accepted" || second[0].Status != "duplicate" || second[1].Status != "duplicate" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	var rawCount, outboxCount int64
	if err := db.Model(&models.RecommendationEvent{}).Where("request_id = ?", requestID).Count(&rawCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxEvent{}).
		Where("event_type = ? AND aggregate_id = ?", eventing.EventTypeRecommendationEventsRecorded, userAggregateID).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if rawCount != 2 || outboxCount != 1 {
		t.Fatalf("raw=%d outbox=%d", rawCount, outboxCount)
	}
}
