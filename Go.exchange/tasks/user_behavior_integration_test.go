package tasks

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserBehaviorProjectionBatchesViewsAndReactionsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ConsumerInbox{}, &models.ArticleBehavior{}, &models.ArticleReaction{}); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	groupID := "test-user-behavior-" + uuid.NewString()
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.UserBehaviorGroupID = groupID
	global.Db = db
	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Where("user_id IN ?", []uint{701, 702}).Delete(&models.ArticleBehavior{})
		db.Where("user_id IN ?", []uint{701, 702}).Delete(&models.ArticleReaction{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	viewMessages := make([]kafka.Message, 0, 8)
	viewMessages = append(viewMessages,
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-1", 701, 801, base.Add(2*time.Minute))),
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-2", 701, 801, base)),
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-3", 701, 801, base.Add(time.Minute))),
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-4", 701, 802, base.Add(time.Minute))),
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-5", 701, 802, base.Add(3*time.Minute))),
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-6", 701, 803, base)),
		kafka.Message{Value: []byte("{malformed")},
		userBehaviorMessage(t, mustArticleViewedEvent(t, "view-7", 701, 803, base.Add(time.Minute))),
	)
	duplicate := userBehaviorMessage(t, mustArticleViewedEvent(t, "view-8", 701, 804, base))
	viewMessages = append(viewMessages, duplicate, duplicate)

	if err := applyUserBehaviorBatch(viewMessages); err != nil {
		t.Fatal(err)
	}
	if err := applyUserBehaviorBatch(viewMessages); err != nil {
		t.Fatal(err)
	}

	assertViewBehaviorCount(t, db, 701, 801, 3, base.Add(2*time.Minute))
	assertViewBehaviorCount(t, db, 701, 802, 2, base.Add(3*time.Minute))
	assertViewBehaviorCount(t, db, 701, 803, 2, base.Add(time.Minute))
	assertViewBehaviorCount(t, db, 701, 804, 1, base)

	lateView := userBehaviorMessage(t, mustArticleViewedEvent(t, "view-9", 701, 801, base.Add(-time.Hour)))
	if err := applyUserBehaviorBatch([]kafka.Message{lateView}); err != nil {
		t.Fatal(err)
	}
	assertViewBehaviorCount(t, db, 701, 801, 4, base.Add(2*time.Minute))

	reactionMessages := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-v5", eventing.EventTypeArticleLiked, 701, 805, 5, base)),
		userBehaviorMessage(t, mustLikeEvent("reaction-v7", eventing.EventTypeArticleUnliked, 701, 805, 7, base.Add(time.Minute))),
		userBehaviorMessage(t, mustLikeEvent("reaction-v6", eventing.EventTypeArticleLiked, 701, 805, 6, base.Add(2*time.Minute))),
	}
	if err := applyUserBehaviorBatch(reactionMessages); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, 701, 805, false, 7, base.Add(time.Minute))

	if err := db.Create(&models.ArticleReaction{
		UserID: 702, ArticleID: 806, Reaction: models.ArticleReactionLike,
		Liked: true, Version: 10, UpdatedAt: base, StateChangedAt: base,
	}).Error; err != nil {
		t.Fatal(err)
	}
	staleReactions := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-v8", eventing.EventTypeArticleLiked, 702, 806, 8, base.Add(time.Minute))),
		userBehaviorMessage(t, mustLikeEvent("reaction-v9", eventing.EventTypeArticleUnliked, 702, 806, 9, base.Add(2*time.Minute))),
	}
	if err := applyUserBehaviorBatch(staleReactions); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, 702, 806, true, 10, base)

	tieReactions := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-tie-like", eventing.EventTypeArticleLiked, 702, 807, 12, base)),
		userBehaviorMessage(t, mustLikeEvent("reaction-tie-unlike", eventing.EventTypeArticleUnliked, 702, 807, 12, base.Add(time.Minute))),
	}
	if err := applyUserBehaviorBatch(tieReactions); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, 702, 807, true, 12, base)
}

func mustArticleViewedEvent(t *testing.T, id string, userID, articleID uint, occurredAt time.Time) eventing.Envelope {
	t.Helper()
	event, err := eventing.NewArticleViewedEnvelope(uuid.NewString(), userID, articleID, occurredAt, "article_detail")
	if err != nil {
		t.Fatal(err)
	}
	event.ID = id
	return event
}

func mustLikeEvent(id, eventType string, userID, articleID uint, version int64, occurredAt time.Time) eventing.Envelope {
	event, err := eventing.NewLikeBehaviorEnvelope(id, userID, articleID, map[string]string{
		eventing.EventTypeArticleLiked:   "like",
		eventing.EventTypeArticleUnliked: "unlike",
	}[eventType], version, occurredAt)
	if err != nil {
		panic(err)
	}
	return event
}

func userBehaviorMessage(t *testing.T, event eventing.Envelope) kafka.Message {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: body}
}

func assertViewBehaviorCount(t *testing.T, db *gorm.DB, userID, articleID uint, count int64, lastSeenAt time.Time) {
	t.Helper()
	var behavior models.ArticleBehavior
	if err := db.Where("user_id = ? AND article_id = ? AND action = ?", userID, articleID, "view").First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != count || !behavior.LastSeenAt.Equal(lastSeenAt) {
		t.Fatalf("behavior=%#v want count=%d last_seen_at=%s", behavior, count, lastSeenAt)
	}
}

func assertReaction(t *testing.T, db *gorm.DB, userID, articleID uint, liked bool, version int64, stateChangedAt time.Time) {
	t.Helper()
	var reaction models.ArticleReaction
	if err := db.Where("user_id = ? AND article_id = ?", userID, articleID).First(&reaction).Error; err != nil {
		t.Fatal(err)
	}
	if reaction.Liked != liked || reaction.Version != version || !reaction.StateChangedAt.Equal(stateChangedAt) {
		t.Fatalf("reaction=%#v want liked=%t version=%d state_changed_at=%s", reaction, liked, version, stateChangedAt)
	}
}
