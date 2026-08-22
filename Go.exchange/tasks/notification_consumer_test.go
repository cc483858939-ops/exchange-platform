package tasks

import (
	"encoding/json"
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

func notificationMessage(t *testing.T, envelope eventing.Envelope) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: "goexchange.activity.events.v1", Partition: 2, Offset: 8, Value: raw}
}

func TestDecodeNotificationActivityMapsDomainEvents(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		make       func(string) (eventing.Envelope, error)
		typeName   string
		dedupeKey  string
		articleID  uint
		commentID  uint
		recipient  uint
		actor      uint
		sourceVers int64
	}{
		{
			name: "like",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewArticleReactionAppliedEnvelope(id, eventing.ArticleReactionAppliedPayload{
					ActorID: 7, ArticleID: 42, ArticleAuthorID: 9, Liked: true, ReactionVersion: 3, StateChangedAt: now,
				})
			},
			typeName: models.NotificationTypePostLiked, dedupeKey: "post_like:7:42", articleID: 42, recipient: 9, actor: 7, sourceVers: 3,
		},
		{
			name: "comment",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewCommentCreatedEnvelope(id, eventing.CommentCreatedPayload{
					CommentID: 11, ArticleID: 42, ActorID: 7, ArticleAuthorID: 9, CreatedAt: now,
				})
			},
			typeName: models.NotificationTypePostReplied, dedupeKey: "post_reply:11", articleID: 42, commentID: 11, recipient: 9, actor: 7,
		},
		{
			name: "follow",
			make: func(id string) (eventing.Envelope, error) {
				return eventing.NewUserFollowCreatedEnvelope(id, eventing.UserFollowCreatedPayload{
					FollowID: 13, FollowerID: 7, FollowingID: 9, CreatedAt: now,
				})
			},
			typeName: models.NotificationTypeUserFollowed, dedupeKey: "user_followed:7:9", recipient: 9, actor: 7, sourceVers: 13,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := test.make(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			record, err := decodeNotificationActivity(notificationMessage(t, envelope))
			if err != nil {
				t.Fatal(err)
			}
			if record.Candidate == nil {
				t.Fatal("expected notification candidate")
			}
			candidate := record.Candidate
			if candidate.Type != test.typeName || candidate.DedupeKey != test.dedupeKey || candidate.RecipientID != test.recipient || candidate.ActorID != test.actor || candidate.SourceVersion != test.sourceVers {
				t.Fatalf("candidate=%+v", candidate)
			}
			if test.articleID == 0 && candidate.ArticleID != nil || test.articleID != 0 && (candidate.ArticleID == nil || *candidate.ArticleID != test.articleID) {
				t.Fatalf("article_id=%v", candidate.ArticleID)
			}
			if test.commentID == 0 && candidate.CommentID != nil || test.commentID != 0 && (candidate.CommentID == nil || *candidate.CommentID != test.commentID) {
				t.Fatalf("comment_id=%v", candidate.CommentID)
			}
		})
	}
}

func TestDecodeNotificationActivitySuppressesValidNoOpEvents(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		envelope func(string) (eventing.Envelope, error)
	}{
		{
			name: "unlike",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewArticleReactionAppliedEnvelope(id, eventing.ArticleReactionAppliedPayload{
					ActorID: 7, ArticleID: 42, ArticleAuthorID: 9, Liked: false, ReactionVersion: 2, StateChangedAt: now,
				})
			},
		},
		{
			name: "self-like",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewArticleReactionAppliedEnvelope(id, eventing.ArticleReactionAppliedPayload{
					ActorID: 9, ArticleID: 42, ArticleAuthorID: 9, Liked: true, ReactionVersion: 1, StateChangedAt: now,
				})
			},
		},
		{
			name: "self-comment",
			envelope: func(id string) (eventing.Envelope, error) {
				return eventing.NewCommentCreatedEnvelope(id, eventing.CommentCreatedPayload{
					CommentID: 11, ArticleID: 42, ActorID: 9, ArticleAuthorID: 9, CreatedAt: now,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := test.envelope(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			record, err := decodeNotificationActivity(notificationMessage(t, envelope))
			if err != nil {
				t.Fatal(err)
			}
			if record.Candidate != nil {
				t.Fatalf("candidate=%+v, want valid no-op", record.Candidate)
			}
		})
	}
}

func TestDecodeNotificationActivityRejectsMalformedRelevantEnvelope(t *testing.T) {
	envelope, err := eventing.NewCommentCreatedEnvelope(uuid.NewString(), eventing.CommentCreatedPayload{
		CommentID: 11, ArticleID: 42, ActorID: 7, ArticleAuthorID: 9, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope.ID = "not-a-uuid"
	if _, err := decodeNotificationActivity(notificationMessage(t, envelope)); err == nil {
		t.Fatal("expected malformed event id to be rejected")
	}
}
