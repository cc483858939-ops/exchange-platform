package tasks

import (
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"
	"github.com/google/uuid"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLikeProjectionRejectsStaleVersionsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.ConsumerInbox{}, &models.ArticleReaction{}, &models.ArticleBehavior{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.LikeSnapshotGroupID = "like-snapshot-integration"
	defer func() { global.Db = originalDB; config.AppConfig = originalConfig }()

	author := models.User{Username: "like-projection-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Article{AuthorID: author.ID, Title: "t", Content: "c", Preview: "p"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	directReaction := models.ArticleReaction{
		UserID: 12, ArticleID: article.ID + 1, Reaction: models.ArticleReactionLike,
		Liked: false, Version: 1, StateChangedAt: now,
	}
	if err := db.Create(&directReaction).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ? AND article_id = ?", directReaction.UserID, directReaction.ArticleID).Delete(&models.ArticleReaction{})
	})
	var reloadedDirect models.ArticleReaction
	if err := db.First(&reloadedDirect, "user_id = ? AND article_id = ?", directReaction.UserID, directReaction.ArticleID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedDirect.Liked {
		t.Fatalf("direct Liked=false persistence returned true: %+v", reloadedDirect)
	}
	like := eventing.UserBehaviorPayload{UserID: 11, ArticleID: article.ID, LikeVersion: 3}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleLiked, like, now)
	}); err != nil {
		t.Fatal(err)
	}
	staleUnlike := like
	staleUnlike.LikeVersion = 2
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleUnliked, staleUnlike, now.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}

	var reaction models.ArticleReaction
	if err := db.First(&reaction, "user_id = ? AND article_id = ?", 11, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reaction.Liked || reaction.Version != 3 || !reaction.StateChangedAt.Equal(now) {
		t.Fatalf("stale reaction overwrote state: %+v", reaction)
	}

	freshUnlike := like
	freshUnlike.LikeVersion = 4
	unlikeAt := now.Add(2 * time.Second)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleUnliked, freshUnlike, unlikeAt)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reaction, "user_id = ? AND article_id = ?", 11, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reaction.Liked || reaction.Version != 4 || !reaction.StateChangedAt.Equal(unlikeAt) {
		t.Fatalf("fresh reaction not applied with event time: %+v", reaction)
	}

	var likeBehaviors int64
	if err := db.Model(&models.ArticleBehavior{}).
		Where("user_id = ? AND article_id = ? AND action = ?", 11, article.ID, "like").
		Count(&likeBehaviors).Error; err != nil {
		t.Fatal(err)
	}
	if likeBehaviors != 0 {
		t.Fatalf("like projection should not create ArticleBehavior rows: %d", likeBehaviors)
	}
}
