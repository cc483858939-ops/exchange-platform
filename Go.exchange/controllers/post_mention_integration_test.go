package controllers

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestPersistPostMentionsStoresRecentAndOutboxAtomicallyIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.UserMentionRecent{}, &models.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	originalConfig := config.AppConfig
	config.AppConfig = &config.Config{Kafka: config.KafkaConfig{ActivityEventsTopic: "activity-test"}}
	t.Cleanup(func() { config.AppConfig = originalConfig })

	token := uuid.NewString()
	actor := models.User{Username: "mention-actor-" + token, Password: "test"}
	replyTarget := models.User{Username: "mention-reply-target-" + token, Password: "test"}
	thirdParty := models.User{Username: "mention-third-party-" + token, Password: "test"}
	deletedUser := models.User{Username: "mention-deleted-" + token, Password: "test"}
	capUsers := make([]models.User, 6)
	users := []*models.User{&actor, &replyTarget, &thirdParty, &deletedUser}
	for i := range capUsers {
		letter := string(rune('a' + i))
		username := "mention-limit-" + letter + "-" + token
		if i == 0 {
			username = "MentionCase-" + token
		} else if i == 1 {
			username = "mentioncase-" + token
		}
		capUsers[i] = models.User{Username: username, Password: "test"}
		users = append(users, &capUsers[i])
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := make([]uint, 0, 4)
	t.Cleanup(func() {
		for _, postID := range postIDs {
			db.Where("aggregate_type = ? AND aggregate_id LIKE ?", "post_mention", strconvUint(postID)+":%").Delete(&models.OutboxEvent{})
		}
		if len(postIDs) > 0 {
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		userIDs := []uint{actor.ID, replyTarget.ID, thirdParty.ID, deletedUser.ID}
		for _, user := range capUsers {
			userIDs = append(userIDs, user.ID)
		}
		db.Where("user_id = ? OR mentioned_user_id IN ?", actor.ID, userIDs).Delete(&models.UserMentionRecent{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})
	if err := db.Delete(&deletedUser).Error; err != nil {
		t.Fatal(err)
	}

	firstAt := time.Now().UTC().Truncate(time.Microsecond)
	var firstPost models.Post
	err = db.Transaction(func(tx *gorm.DB) error {
		firstPost = models.Post{AuthorID: actor.ID, Content: "mention test", Visibility: "public"}
		if err := tx.Create(&firstPost).Error; err != nil {
			return err
		}
		return persistPostMentions(tx, firstPost.ID, actor.ID,
			[]string{replyTarget.Username, replyTarget.Username, thirdParty.Username, actor.Username, deletedUser.Username, "missing-" + token},
			firstAt, replyTarget.ID, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, firstPost.ID)
	var recents []models.UserMentionRecent
	if err := db.Where("user_id = ?", actor.ID).Order("mentioned_user_id").Find(&recents).Error; err != nil {
		t.Fatal(err)
	}
	if len(recents) != 2 || recents[0].MentionedUserID != replyTarget.ID || recents[1].MentionedUserID != thirdParty.ID {
		t.Fatalf("recent mention rows=%+v", recents)
	}
	var firstEvents []models.OutboxEvent
	if err := db.Where("aggregate_type = ? AND aggregate_id LIKE ?", "post_mention", strconvUint(firstPost.ID)+":%").Find(&firstEvents).Error; err != nil {
		t.Fatal(err)
	}
	if len(firstEvents) != 1 {
		t.Fatalf("reply-target suppression and duplicate tokens produced %d mention events", len(firstEvents))
	}
	var envelope eventing.Envelope
	if err := json.Unmarshal([]byte(firstEvents[0].Message), &envelope); err != nil || envelope.Type != eventing.EventTypePostMentioned || envelope.AggregateID != strconvUint(firstPost.ID)+":"+strconvUint(thirdParty.ID) {
		t.Fatalf("mention outbox envelope=%+v err=%v", envelope, err)
	}
	casePost := models.Post{AuthorID: actor.ID, Content: "case match", Visibility: "public"}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&casePost).Error; err != nil {
			return err
		}
		return persistPostMentions(tx, casePost.ID, actor.ID, []string{capUsers[0].Username}, firstAt, 0, 0)
	}); err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, casePost.ID)
	var exactCaseRows []models.UserMentionRecent
	if err := db.Where("user_id = ? AND mentioned_user_id IN ?", actor.ID, []uint{capUsers[0].ID, capUsers[1].ID}).Find(&exactCaseRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(exactCaseRows) != 1 || exactCaseRows[0].MentionedUserID != capUsers[0].ID {
		t.Fatalf("case-sensitive username resolution rows=%+v", exactCaseRows)
	}

	secondAt := firstAt.Add(2 * time.Minute)
	var secondPost models.Post
	if err := db.Transaction(func(tx *gorm.DB) error {
		secondPost = models.Post{AuthorID: actor.ID, Content: "mention refresh", Visibility: "public"}
		if err := tx.Create(&secondPost).Error; err != nil {
			return err
		}
		return persistPostMentions(tx, secondPost.ID, actor.ID, []string{thirdParty.Username}, secondAt, 0, 0)
	}); err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, secondPost.ID)
	oldPost := models.Post{Model: gorm.Model{CreatedAt: firstAt, UpdatedAt: firstAt}, AuthorID: actor.ID, Content: "older mention", Visibility: "public"}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&oldPost).Error; err != nil {
			return err
		}
		return persistPostMentions(tx, oldPost.ID, actor.ID, []string{thirdParty.Username}, firstAt, 0, 0)
	}); err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, oldPost.ID)
	var thirdRecent models.UserMentionRecent
	if err := db.Where("user_id = ? AND mentioned_user_id = ?", actor.ID, thirdParty.ID).First(&thirdRecent).Error; err != nil || !thirdRecent.LastMentionedAt.Equal(secondAt) {
		t.Fatalf("last mention time=%s err=%v want=%s", thirdRecent.LastMentionedAt, err, secondAt)
	}

	failureConfig := &config.Config{}
	config.AppConfig = failureConfig
	var rolledBackPostID uint
	err = db.Transaction(func(tx *gorm.DB) error {
		post := models.Post{AuthorID: actor.ID, Content: "rollback @" + replyTarget.Username, Visibility: "public"}
		if err := tx.Create(&post).Error; err != nil {
			return err
		}
		rolledBackPostID = post.ID
		return persistPostMentions(tx, post.ID, actor.ID, []string{replyTarget.Username}, secondAt.Add(time.Hour), 0, 0)
	})
	if err == nil {
		t.Fatal("missing Activity topic did not fail mention Outbox write")
	}
	var rolledBack models.Post
	if err := db.Unscoped().First(&rolledBack, rolledBackPostID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("post survived failed mention outbox transaction: err=%v", err)
	}
	if err := db.Where("user_id = ? AND mentioned_user_id = ?", actor.ID, replyTarget.ID).First(&thirdRecent).Error; err != nil || !thirdRecent.LastMentionedAt.Equal(firstAt) {
		t.Fatalf("failed outbox changed recent mention: row=%+v err=%v", thirdRecent, err)
	}

	var tooManyPost models.Post
	capTokens := make([]string, 0, len(capUsers))
	capIDs := make([]uint, 0, len(capUsers))
	for _, user := range capUsers {
		capTokens = append(capTokens, user.Username)
		capIDs = append(capIDs, user.ID)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		tooManyPost = models.Post{AuthorID: actor.ID, Content: "too many recipients", Visibility: "public"}
		if err := tx.Create(&tooManyPost).Error; err != nil {
			return err
		}
		return persistPostMentions(tx, tooManyPost.ID, actor.ID, capTokens, secondAt.Add(2*time.Hour), 0, 0)
	})
	if !errors.Is(err, errTooManyPostMentions) {
		t.Fatalf("six valid mention recipients error=%v want=%v", err, errTooManyPostMentions)
	}
	var rolledBackCapPost models.Post
	if err := db.Unscoped().First(&rolledBackCapPost, tooManyPost.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("post survived over-limit mention rollback: err=%v", err)
	}
	var capRecents int64
	if err := db.Model(&models.UserMentionRecent{}).Where("user_id = ? AND mentioned_user_id IN ?", actor.ID, capIDs).Count(&capRecents).Error; err != nil || capRecents != 0 {
		t.Fatalf("over-limit request persisted recent rows=%d err=%v", capRecents, err)
	}
}

func TestConcurrentUserMentionRecentUpsertsKeepLatestAndIsolateUsersIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserMentionRecent{}); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	actor := models.User{Username: "mention-upsert-actor-" + token, Password: "test"}
	otherActor := models.User{Username: "mention-upsert-other-" + token, Password: "test"}
	recipient := models.User{Username: "mention-upsert-recipient-" + token, Password: "test"}
	if err := db.Create(&[]*models.User{&actor, &otherActor, &recipient}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{actor.ID, otherActor.ID, recipient.ID}
		if err := db.Where("user_id IN ? OR mentioned_user_id IN ?", ids, ids).Delete(&models.UserMentionRecent{}).Error; err != nil {
			t.Errorf("clean mention recent rows: %v", err)
		}
		if err := db.Unscoped().Where("id IN ?", ids).Delete(&models.User{}).Error; err != nil {
			t.Errorf("clean mention users: %v", err)
		}
	})

	const writers = 8
	base := time.Now().UTC().Truncate(time.Microsecond)
	start := make(chan struct{})
	results := make(chan error, writers)
	var wait sync.WaitGroup
	for i := 0; i < writers; i++ {
		mentionedAt := base.Add(time.Duration(i) * time.Second)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- db.Transaction(func(tx *gorm.DB) error {
				return upsertUserMentionRecents(tx, actor.ID, []uint{recipient.ID}, mentionedAt)
			})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}

	var actorRows []models.UserMentionRecent
	if err := db.Where("user_id = ?", actor.ID).Find(&actorRows).Error; err != nil {
		t.Fatal(err)
	}
	wantLatest := base.Add((writers - 1) * time.Second)
	if len(actorRows) != 1 || actorRows[0].MentionedUserID != recipient.ID || !actorRows[0].LastMentionedAt.Equal(wantLatest) {
		t.Fatalf("concurrent actor rows=%+v want one row at %s", actorRows, wantLatest)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return upsertUserMentionRecents(tx, otherActor.ID, []uint{recipient.ID}, base)
	}); err != nil {
		t.Fatal(err)
	}
	var isolated models.UserMentionRecent
	if err := db.Where("user_id = ? AND mentioned_user_id = ?", actor.ID, recipient.ID).First(&isolated).Error; err != nil || !isolated.LastMentionedAt.Equal(wantLatest) {
		t.Fatalf("other user's mention changed actor record=%+v err=%v", isolated, err)
	}
}
