package tasks

import (
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

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
	if err := db.AutoMigrate(&models.Article{}, &models.ConsumerInbox{}, &models.ArticleReaction{}, &models.ArticleBehavior{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.LikeSnapshotGroupID = "like-snapshot-integration"
	defer func() { global.Db = originalDB; config.AppConfig = originalConfig }()
	article := models.Article{Title: "t", Content: "c", Preview: "p"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	newer, _ := eventing.NewLikeSnapshotEnvelope(article.ID, 8, 2)
	if err := applyLikeSnapshotEvent(newer); err != nil {
		t.Fatal(err)
	}
	older, _ := eventing.NewLikeSnapshotEnvelope(article.ID, 1, 1)
	if err := applyLikeSnapshotEvent(older); err != nil {
		t.Fatal(err)
	}
	var loaded models.Article
	if err := db.First(&loaded, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.LikeCount != 8 || loaded.LikeSyncVersion != 2 {
		t.Fatalf("stale snapshot overwrote article: %+v", loaded)
	}
	like := eventing.UserBehaviorPayload{UserID: 11, ArticleID: article.ID, LikeVersion: 3}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleLiked, like)
	}); err != nil {
		t.Fatal(err)
	}
	staleUnlike := like
	staleUnlike.LikeVersion = 2
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleUnliked, staleUnlike)
	}); err != nil {
		t.Fatal(err)
	}
	var reaction models.ArticleReaction
	if err := db.First(&reaction, "user_id = ? AND article_id = ?", 11, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reaction.Liked || reaction.Version != 3 {
		t.Fatalf("stale reaction overwrote state: %+v", reaction)
	}
	freshUnlike := like
	freshUnlike.LikeVersion = 4
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyArticleReactionProjection(tx, eventing.EventTypeArticleUnliked, freshUnlike)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reaction, "user_id = ? AND article_id = ?", 11, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reaction.Liked || reaction.Version != 4 {
		t.Fatalf("fresh reaction not applied: %+v", reaction)
	}

	now := time.Now().UTC()
	behaviorLike := eventing.UserBehaviorPayload{UserID: 12, ArticleID: article.ID, LikeVersion: 5}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyLikeBehaviorProjection(tx, eventing.EventTypeArticleLiked, behaviorLike, now)
	}); err != nil {
		t.Fatal(err)
	}
	staleBehaviorUnlike := behaviorLike
	staleBehaviorUnlike.LikeVersion = 4
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyLikeBehaviorProjection(tx, eventing.EventTypeArticleUnliked, staleBehaviorUnlike, now.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	var behavior models.ArticleBehavior
	if err := db.First(&behavior, "user_id = ? AND article_id = ? AND action = ?", 12, article.ID, "like").Error; err != nil {
		t.Fatal(err)
	}
	if !behavior.Active || behavior.BehaviorVersion != 5 || behavior.Count != 1 {
		t.Fatalf("stale behavior overwrote state: %+v", behavior)
	}
	freshBehaviorUnlike := behaviorLike
	freshBehaviorUnlike.LikeVersion = 6
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyLikeBehaviorProjection(tx, eventing.EventTypeArticleUnliked, freshBehaviorUnlike, now.Add(2*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&behavior, behavior.ID).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Active || behavior.BehaviorVersion != 6 || behavior.Count != 0 {
		t.Fatalf("fresh behavior not applied: %+v", behavior)
	}
}
