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
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostReaction{}, &models.PostBehavior{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.LikeSnapshotGroupID = "like-snapshot-integration"
	defer func() { global.Db = originalDB; config.AppConfig = originalConfig }()

	userA := models.User{Username: "like-projection-a-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&userA).Error; err != nil {
		t.Fatal(err)
	}
	userIDs := []uint{userA.ID}
	postIDs := make([]uint, 0, 2)
	t.Cleanup(func() {
		if len(postIDs) > 0 {
			db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
			db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		if len(userIDs) > 0 {
			db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostReaction{})
			db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.PostBehavior{})
			db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
		}
	})

	userB := models.User{Username: "like-projection-b-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&userB).Error; err != nil {
		t.Fatal(err)
	}
	userIDs = append(userIDs, userB.ID)
	postOne := models.Post{AuthorID: userA.ID, Content: "post one", Visibility: "public"}
	if err := db.Create(&postOne).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, postOne.ID)
	postTwo := models.Post{AuthorID: userA.ID, Content: "post two", Visibility: "public"}
	if err := db.Create(&postTwo).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, postTwo.ID)

	now := time.Now().UTC().Truncate(time.Microsecond)
	directReaction := models.PostReaction{
		UserID: userB.ID, PostID: postTwo.ID, Reaction: models.PostReactionLike,
		Liked: false, Version: 1, StateChangedAt: now,
	}
	if err := db.Create(&directReaction).Error; err != nil {
		t.Fatal(err)
	}
	var reloadedDirect models.PostReaction
	if err := db.First(&reloadedDirect, "user_id = ? AND post_id = ?", directReaction.UserID, directReaction.PostID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedDirect.Liked {
		t.Fatalf("direct Liked=false persistence returned true: %+v", reloadedDirect)
	}
	like := eventing.UserBehaviorPayload{UserID: userB.ID, PostID: postOne.ID, LikeVersion: 3}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPostReactionProjection(tx, eventing.EventTypePostLiked, like, now)
	}); err != nil {
		t.Fatal(err)
	}
	staleUnlike := like
	staleUnlike.LikeVersion = 2
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPostReactionProjection(tx, eventing.EventTypePostUnliked, staleUnlike, now.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}

	var reaction models.PostReaction
	if err := db.First(&reaction, "user_id = ? AND post_id = ?", userB.ID, postOne.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reaction.Liked || reaction.Version != 3 || !reaction.StateChangedAt.Equal(now) {
		t.Fatalf("stale reaction overwrote state: %+v", reaction)
	}

	freshUnlike := like
	freshUnlike.LikeVersion = 4
	unlikeAt := now.Add(2 * time.Second)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyPostReactionProjection(tx, eventing.EventTypePostUnliked, freshUnlike, unlikeAt)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reaction, "user_id = ? AND post_id = ?", userB.ID, postOne.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reaction.Liked || reaction.Version != 4 || !reaction.StateChangedAt.Equal(unlikeAt) {
		t.Fatalf("fresh reaction not applied with event time: %+v", reaction)
	}

	var likeBehaviors int64
	if err := db.Model(&models.PostBehavior{}).
		Where("user_id = ? AND post_id = ? AND action = ?", userB.ID, postOne.ID, "like").
		Count(&likeBehaviors).Error; err != nil {
		t.Fatal(err)
	}
	if likeBehaviors != 0 {
		t.Fatalf("like projection should not create PostBehavior rows: %d", likeBehaviors)
	}
}
