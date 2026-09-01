package tasks

import (
	"os"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestFilterNotificationCandidatesRequiresActiveParticipantsAndValidReplyPost(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Post{}, &models.PostArticle{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}

	recipient := models.User{Username: "notification-filter-recipient-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	userIDs := []uint{recipient.ID}
	postIDs := make([]uint, 0, 6)
	t.Cleanup(func() {
		if len(postIDs) > 0 {
			db.Unscoped().Where("reply_to_post_id IS NOT NULL AND id IN ?", postIDs).Delete(&models.Post{})
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		if len(userIDs) > 0 {
			db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
		}
	})

	deletedRecipient := models.User{Username: "notification-filter-deleted-recipient-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&deletedRecipient).Error; err != nil {
		t.Fatal(err)
	}
	userIDs = append(userIDs, deletedRecipient.ID)
	activeActor := models.User{Username: "notification-filter-active-actor-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&activeActor).Error; err != nil {
		t.Fatal(err)
	}
	userIDs = append(userIDs, activeActor.ID)
	deletedActor := models.User{Username: "notification-filter-deleted-actor-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&deletedActor).Error; err != nil {
		t.Fatal(err)
	}
	userIDs = append(userIDs, deletedActor.ID)

	now := time.Now().UTC()
	root := models.Post{AuthorID: recipient.ID, Content: "notification parent", Visibility: "public"}
	if err := db.Create(&root).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, root.ID)
	otherRoot := models.Post{AuthorID: activeActor.ID, Content: "other notification parent", Visibility: "public"}
	if err := db.Create(&otherRoot).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, otherRoot.ID)
	conversationID := root.ID
	validReply := models.Post{AuthorID: activeActor.ID, ReplyToPostID: &root.ID, ConversationID: &conversationID, Content: "valid reply", Visibility: "public"}
	if err := db.Create(&validReply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, validReply.ID)
	wrongReply := models.Post{AuthorID: activeActor.ID, ReplyToPostID: &otherRoot.ID, ConversationID: &otherRoot.ID, Content: "wrong parent reply", Visibility: "public"}
	if err := db.Create(&wrongReply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, wrongReply.ID)
	deletedReply := models.Post{AuthorID: activeActor.ID, ReplyToPostID: &root.ID, ConversationID: &conversationID, Content: "deleted reply", Visibility: "public"}
	if err := db.Create(&deletedReply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, deletedReply.ID)
	if err := db.Delete(&deletedReply).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Delete(&deletedActor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedRecipient).Error; err != nil {
		t.Fatal(err)
	}

	rootID, validReplyID, wrongReplyID, deletedReplyID := root.ID, validReply.ID, wrongReply.ID, deletedReply.ID
	candidates := []models.Notification{
		{RecipientID: recipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypeUserFollowed, SourceVersion: 1, DedupeKey: "filter:valid-follow", ActivityAt: now},
		{RecipientID: recipient.ID, ActorID: deletedActor.ID, Type: models.NotificationTypeUserFollowed, SourceVersion: 1, DedupeKey: "filter:deleted-actor", ActivityAt: now},
		{RecipientID: deletedRecipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypeUserFollowed, SourceVersion: 1, DedupeKey: "filter:deleted-recipient", ActivityAt: now},
		{RecipientID: recipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypePostReplied, PostID: &validReplyID, SourceVersion: 0, DedupeKey: "filter:valid-reply", ActivityAt: now},
		{RecipientID: recipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypePostReplied, PostID: &rootID, SourceVersion: 0, DedupeKey: "filter:root-not-reply", ActivityAt: now},
		{RecipientID: recipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypePostReplied, PostID: &wrongReplyID, SourceVersion: 0, DedupeKey: "filter:wrong-parent-reply", ActivityAt: now},
		{RecipientID: recipient.ID, ActorID: activeActor.ID, Type: models.NotificationTypePostReplied, PostID: &deletedReplyID, SourceVersion: 0, DedupeKey: "filter:deleted-reply", ActivityAt: now},
	}
	filtered, err := filterNotificationCandidates(db, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered candidates=%d want=2: %+v", len(filtered), filtered)
	}
	seen := make(map[string]bool, len(filtered))
	for _, candidate := range filtered {
		seen[candidate.DedupeKey] = true
	}
	for _, key := range []string{"filter:valid-follow", "filter:valid-reply"} {
		if !seen[key] {
			t.Fatalf("valid candidate %q was filtered: %+v", key, filtered)
		}
	}
	for _, key := range []string{
		"filter:deleted-actor", "filter:deleted-recipient", "filter:root-not-reply",
		"filter:wrong-parent-reply", "filter:deleted-reply",
	} {
		if seen[key] {
			t.Fatalf("invalid candidate %q survived filtering", key)
		}
	}
}
