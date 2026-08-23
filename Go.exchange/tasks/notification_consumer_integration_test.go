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

func TestFilterNotificationCandidatesKeepsHistoricalActorsAndValidatesCommentArticle(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Article{}, &models.Comment{}, &models.Notification{}); err != nil {
		t.Fatal(err)
	}

	recipient := models.User{Username: "notification-filter-recipient-" + uuid.NewString(), Password: "test"}
	deletedRecipient := models.User{Username: "notification-filter-deleted-recipient-" + uuid.NewString(), Password: "test"}
	actor := models.User{Username: "notification-filter-actor-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&[]*models.User{&recipient, &deletedRecipient, &actor}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	article := models.Article{AuthorID: actor.ID, Title: "notification filter", Content: "content", Preview: "preview", PublicationState: "published", PublishedAt: &now}
	otherArticle := models.Article{AuthorID: actor.ID, Title: "other article", Content: "content", Preview: "preview", PublicationState: "published", PublishedAt: &now}
	if err := db.Create(&[]*models.Article{&article, &otherArticle}).Error; err != nil {
		t.Fatal(err)
	}
	comment := models.Comment{ArticleID: article.ID, UserID: actor.ID, Content: "comment"}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedRecipient).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", []uint{comment.ID}).Delete(&models.Comment{})
		db.Unscoped().Where("id IN ?", []uint{article.ID, otherArticle.ID}).Delete(&models.Article{})
		db.Unscoped().Where("id IN ?", []uint{recipient.ID, deletedRecipient.ID, actor.ID}).Delete(&models.User{})
	})

	validFollow := models.Notification{RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypeUserFollowed, DedupeKey: "filter:valid-follow"}
	validReply := models.Notification{RecipientID: recipient.ID, ActorID: actor.ID, Type: models.NotificationTypePostReplied, ArticleID: &article.ID, CommentID: &comment.ID, DedupeKey: "filter:valid-reply"}
	mismatchedReply := validReply
	mismatchedReply.DedupeKey = "filter:mismatched-reply"
	mismatchedReply.ArticleID = &otherArticle.ID
	deletedRecipientCandidate := validFollow
	deletedRecipientCandidate.DedupeKey = "filter:deleted-recipient"
	deletedRecipientCandidate.RecipientID = deletedRecipient.ID

	filtered, err := filterNotificationCandidates(db, []models.Notification{validFollow, validReply, mismatchedReply, deletedRecipientCandidate})
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
	for _, key := range []string{validFollow.DedupeKey, validReply.DedupeKey} {
		if !seen[key] {
			t.Fatalf("valid candidate %q was filtered: %+v", key, filtered)
		}
	}
	for _, key := range []string{mismatchedReply.DedupeKey, deletedRecipientCandidate.DedupeKey} {
		if seen[key] {
			t.Fatalf("invalid candidate %q survived filtering", key)
		}
	}
}
