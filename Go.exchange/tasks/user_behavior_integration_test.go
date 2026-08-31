package tasks

import (
	"encoding/json"
	"errors"
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
	if err := db.AutoMigrate(
		&models.User{},
		&models.Post{},
		&models.ConsumerInbox{},
		&models.PostBehavior{},
		&models.PostReaction{},
		&models.UserRecoProfileDirty{},
	); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	groupID := "test-user-behavior-" + uuid.NewString()
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.UserBehaviorGroupID = groupID
	config.AppConfig.Kafka.ActivityEventsTopic = "goexchange.activity.events.v1"
	global.Db = db

	var userOneID, userTwoID uint
	postIDs := make([]uint, 0, 11)
	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("user_id IN ?", []uint{userOneID, userTwoID}).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id IN ?", []uint{userOneID, userTwoID}).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id IN ?", []uint{userOneID, userTwoID}).Delete(&models.PostReaction{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{userOneID, userTwoID}).Delete(&models.User{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	userOne := models.User{
		Username: "test-user-behavior-one-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&userOne).Error; err != nil {
		t.Fatal(err)
	}
	userOneID = userOne.ID

	userTwo := models.User{
		Username: "test-user-behavior-two-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&userTwo).Error; err != nil {
		t.Fatal(err)
	}
	userTwoID = userTwo.ID
	for index := 0; index < cap(postIDs); index++ {
		article := models.Post{
			AuthorID: userOneID, Content: "user behavior fixture", Visibility: "public",
		}
		if err := db.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
		postIDs = append(postIDs, article.ID)
	}
	postID := func(index int) uint { return postIDs[index] }

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	viewMessages := make([]kafka.Message, 0, 8)
	viewMessages = append(viewMessages,
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-1", userOneID, postID(0), base.Add(2*time.Minute))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-2", userOneID, postID(0), base)),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-3", userOneID, postID(0), base.Add(time.Minute))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-4", userOneID, postID(1), base.Add(time.Minute))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-5", userOneID, postID(1), base.Add(3*time.Minute))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-6", userOneID, postID(2), base)),
		kafka.Message{Value: []byte("{malformed")},
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-7", userOneID, postID(2), base.Add(time.Minute))),
	)
	duplicate := userBehaviorMessage(t, mustPostViewedEvent(t, "view-8", userOneID, postID(3), base))
	viewMessages = append(viewMessages, duplicate, duplicate)

	if err := applyUserBehaviorBatch(viewMessages); err != nil {
		t.Fatal(err)
	}
	var dirty models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", userOneID).First(&dirty).Error; err != nil {
		t.Fatal(err)
	}
	if dirty.DirtyVersion < 1 || dirty.Reason != "user_behavior_projection" {
		t.Fatalf("dirty profile=%#v want version>=1 reason=%q", dirty, "user_behavior_projection")
	}
	if err := applyUserBehaviorBatch(viewMessages); err != nil {
		t.Fatal(err)
	}

	assertViewBehaviorCount(t, db, userOneID, postID(0), 3, base.Add(2*time.Minute))
	assertViewBehaviorCount(t, db, userOneID, postID(1), 2, base.Add(3*time.Minute))
	assertViewBehaviorCount(t, db, userOneID, postID(2), 2, base.Add(time.Minute))
	assertViewBehaviorCount(t, db, userOneID, postID(3), 1, base)

	lateView := userBehaviorMessage(t, mustPostViewedEvent(t, "view-9", userOneID, postID(0), base.Add(-time.Hour)))
	if err := applyUserBehaviorBatch([]kafka.Message{lateView}); err != nil {
		t.Fatal(err)
	}
	assertViewBehaviorCount(t, db, userOneID, postID(0), 4, base.Add(2*time.Minute))

	reactionMessages := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-v5", eventing.EventTypePostLiked, userOneID, postID(4), 5, base)),
		userBehaviorMessage(t, mustLikeEvent("reaction-v7", eventing.EventTypePostUnliked, userOneID, postID(4), 7, base.Add(time.Minute))),
		userBehaviorMessage(t, mustLikeEvent("reaction-v6", eventing.EventTypePostLiked, userOneID, postID(4), 6, base.Add(2*time.Minute))),
	}
	if err := applyUserBehaviorBatch(reactionMessages); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, userOneID, postID(4), false, 7, base.Add(time.Minute))

	if err := db.Create(&models.PostReaction{
		UserID: userTwoID, PostID: postID(5), Reaction: models.PostReactionLike,
		Liked: true, Version: 10, UpdatedAt: base, StateChangedAt: base,
	}).Error; err != nil {
		t.Fatal(err)
	}
	staleReactions := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-v8", eventing.EventTypePostLiked, userTwoID, postID(5), 8, base.Add(time.Minute))),
		userBehaviorMessage(t, mustLikeEvent("reaction-v9", eventing.EventTypePostUnliked, userTwoID, postID(5), 9, base.Add(2*time.Minute))),
	}
	if err := applyUserBehaviorBatch(staleReactions); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, userTwoID, postID(5), true, 10, base)

	tieReactions := []kafka.Message{
		userBehaviorMessage(t, mustLikeEvent("reaction-tie-like", eventing.EventTypePostLiked, userTwoID, postID(6), 12, base)),
		userBehaviorMessage(t, mustLikeEvent("reaction-tie-unlike", eventing.EventTypePostUnliked, userTwoID, postID(6), 12, base.Add(time.Minute))),
	}
	if err := applyUserBehaviorBatch(tieReactions); err != nil {
		t.Fatal(err)
	}
	assertReaction(t, db, userTwoID, postID(6), true, 12, base)

	reactionMatrix := []struct {
		postID             uint
		initialLiked       bool
		initialVersion     int64
		incomingType       string
		incomingVersion    int64
		wantLiked          bool
		wantVersion        int64
		wantStateChangedAt time.Time
	}{
		{postID: postID(7), initialLiked: false, initialVersion: 10, incomingType: eventing.EventTypePostLiked, incomingVersion: 11, wantLiked: true, wantVersion: 11, wantStateChangedAt: base.Add(time.Minute)},
		{postID: postID(8), initialLiked: true, initialVersion: 10, incomingType: eventing.EventTypePostUnliked, incomingVersion: 11, wantLiked: false, wantVersion: 11, wantStateChangedAt: base.Add(time.Minute)},
		{postID: postID(9), initialLiked: false, initialVersion: 12, incomingType: eventing.EventTypePostLiked, incomingVersion: 11, wantLiked: false, wantVersion: 12, wantStateChangedAt: base},
		{postID: postID(10), initialLiked: true, initialVersion: 12, incomingType: eventing.EventTypePostUnliked, incomingVersion: 11, wantLiked: true, wantVersion: 12, wantStateChangedAt: base},
	}
	for _, test := range reactionMatrix {
		if err := db.Create(&models.PostReaction{
			UserID: userTwoID, PostID: test.postID, Reaction: models.PostReactionLike,
			Liked: test.initialLiked, Version: test.initialVersion, UpdatedAt: base, StateChangedAt: base,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := applyUserBehaviorBatch([]kafka.Message{
			userBehaviorMessage(t, mustLikeEvent(uuid.NewString(), test.incomingType, userTwoID, test.postID, test.incomingVersion, base.Add(time.Minute))),
		}); err != nil {
			t.Fatal(err)
		}
		assertReaction(t, db, userTwoID, test.postID, test.wantLiked, test.wantVersion, test.wantStateChangedAt)
	}
}

func TestUserBehaviorProjectionUpdatesPublicViewCountIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostBehavior{}, &models.PostReaction{}, &models.UserRecoProfileDirty{}); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	groupID := "test-public-view-count-" + uuid.NewString()
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.UserBehaviorGroupID = groupID
	config.AppConfig.Kafka.ActivityEventsTopic = "goexchange.activity.events.v1"
	global.Db = db

	viewerOne := models.User{Username: "view-count-one-" + uuid.NewString(), Password: "test"}
	viewerTwo := models.User{Username: "view-count-two-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewerOne).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&viewerTwo).Error; err != nil {
		t.Fatal(err)
	}
	postOne := models.Post{AuthorID: viewerOne.ID, Content: "view one", Visibility: "public"}
	postTwo := models.Post{AuthorID: viewerOne.ID, Content: "view two", Visibility: "public"}
	postThree := models.Post{AuthorID: viewerOne.ID, Content: "view three", Visibility: "public"}
	if err := db.Create(&postOne).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&postTwo).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&postThree).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("user_id IN ?", []uint{viewerOne.ID, viewerTwo.ID}).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("post_id IN ?", []uint{postOne.ID, postTwo.ID, postThree.ID}).Delete(&models.PostBehavior{})
		db.Unscoped().Where("user_id IN ?", []uint{viewerOne.ID, viewerTwo.ID}).Delete(&models.PostReaction{})
		db.Unscoped().Where("id IN ?", []uint{postOne.ID, postTwo.ID, postThree.ID}).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", []uint{viewerOne.ID, viewerTwo.ID}).Delete(&models.User{})
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	base := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	firstBatch := []kafka.Message{
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-1", viewerOne.ID, postOne.ID, base)),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-2", viewerOne.ID, postOne.ID, base.Add(time.Second))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-3", viewerOne.ID, postOne.ID, base.Add(2*time.Second))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-4", viewerOne.ID, postTwo.ID, base)),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-5", viewerOne.ID, postTwo.ID, base.Add(time.Second))),
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-count-6", viewerTwo.ID, postOne.ID, base)),
		userBehaviorMessage(t, mustLikeEvent("view-count-like", eventing.EventTypePostLiked, viewerOne.ID, postThree.ID, 1, base)),
	}
	if err := applyUserBehaviorBatch(firstBatch); err != nil {
		t.Fatal(err)
	}
	if err := applyUserBehaviorBatch(firstBatch); err != nil {
		t.Fatal(err)
	}

	assertViewBehaviorCount(t, db, viewerOne.ID, postOne.ID, 3, base.Add(2*time.Second))
	assertViewBehaviorCount(t, db, viewerOne.ID, postTwo.ID, 2, base.Add(time.Second))
	assertViewBehaviorCount(t, db, viewerTwo.ID, postOne.ID, 1, base)
	assertPostViewCount(t, db, postOne.ID, 4)
	assertPostViewCount(t, db, postTwo.ID, 2)
	assertPostViewCount(t, db, postThree.ID, 0)
	var reaction models.PostReaction
	if err := db.Where("user_id = ? AND post_id = ?", viewerOne.ID, postThree.ID).First(&reaction).Error; err != nil {
		t.Fatal(err)
	}
}

func TestUserBehaviorProjectionRollsBackViewCountAndInboxOnFailureIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.ConsumerInbox{}, &models.PostBehavior{}, &models.UserRecoProfileDirty{}); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig, originalIncrement := global.Db, config.AppConfig, incrementPostViewCounts
	groupID := "test-public-view-rollback-" + uuid.NewString()
	config.AppConfig = &config.Config{}
	config.AppConfig.Kafka.UserBehaviorGroupID = groupID
	global.Db = db
	incrementPostViewCounts = func(*gorm.DB, map[uint]int64) error {
		return errors.New("injected view count failure")
	}
	t.Cleanup(func() {
		incrementPostViewCounts = originalIncrement
		global.Db, config.AppConfig = originalDB, originalConfig
	})

	author := models.User{Username: "view-rollback-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{AuthorID: author.ID, Content: "rollback", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("consumer_name = ?", groupID).Delete(&models.ConsumerInbox{})
		db.Unscoped().Where("user_id = ?", author.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Delete(&article)
		db.Unscoped().Delete(&author)
	})

	err = applyUserBehaviorBatch([]kafka.Message{
		userBehaviorMessage(t, mustPostViewedEvent(t, "view-rollback", author.ID, article.ID, time.Now().UTC())),
	})
	if err == nil {
		t.Fatal("expected injected view count failure")
	}
	var inboxCount, behaviorCount int64
	if err := db.Model(&models.ConsumerInbox{}).Where("consumer_name = ?", groupID).Count(&inboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.PostBehavior{}).Where("post_id = ?", article.ID).Count(&behaviorCount).Error; err != nil {
		t.Fatal(err)
	}
	var reloaded models.Post
	if err := db.First(&reloaded, article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if inboxCount != 0 || behaviorCount != 0 || reloaded.ViewCount != 0 {
		t.Fatalf("rollback inbox=%d behavior=%d article=%#v", inboxCount, behaviorCount, reloaded)
	}
}

func assertPostViewCount(t *testing.T, db *gorm.DB, postID uint, want int64) {
	t.Helper()
	var article models.Post
	if err := db.Select("view_count").First(&article, postID).Error; err != nil {
		t.Fatal(err)
	}
	if article.ViewCount != want {
		t.Fatalf("article %d view_count=%d want=%d", postID, article.ViewCount, want)
	}
}

func mustPostViewedEvent(t *testing.T, id string, userID, postID uint, occurredAt time.Time) eventing.Envelope {
	t.Helper()
	event, err := eventing.NewPostViewedEnvelope(uuid.NewString(), userID, postID, occurredAt, "post_detail")
	if err != nil {
		t.Fatal(err)
	}
	event.ID = id
	return event
}

func mustLikeEvent(id, eventType string, userID, postID uint, version int64, occurredAt time.Time) eventing.Envelope {
	event, err := eventing.NewLikeBehaviorEnvelope(id, userID, postID, map[string]string{
		eventing.EventTypePostLiked:   "like",
		eventing.EventTypePostUnliked: "unlike",
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

func assertViewBehaviorCount(t *testing.T, db *gorm.DB, userID, postID uint, count int64, lastSeenAt time.Time) {
	t.Helper()
	var behavior models.PostBehavior
	if err := db.Where("user_id = ? AND post_id = ? AND action = ?", userID, postID, "view").First(&behavior).Error; err != nil {
		t.Fatal(err)
	}
	if behavior.Count != count || !behavior.LastSeenAt.Equal(lastSeenAt) {
		t.Fatalf("behavior=%#v want count=%d last_seen_at=%s", behavior, count, lastSeenAt)
	}
}

func assertReaction(t *testing.T, db *gorm.DB, userID, postID uint, liked bool, version int64, stateChangedAt time.Time) {
	t.Helper()
	var reaction models.PostReaction
	if err := db.Where("user_id = ? AND post_id = ?", userID, postID).First(&reaction).Error; err != nil {
		t.Fatal(err)
	}
	if reaction.Liked != liked || reaction.Version != version || !reaction.StateChangedAt.Equal(stateChangedAt) {
		t.Fatalf("reaction=%#v want liked=%t version=%d state_changed_at=%s", reaction, liked, version, stateChangedAt)
	}
}
