package recommendation

import (
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"
	"gorm.io/gorm"
)

func TestCanonicalizeOutcomesReturnsCompleteInteractionSet(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	qualified := "qualified"
	result := CanonicalizeOutcomes(
		[]models.ArticleBehavior{
			{Model: gorm.Model{ID: 1}, ArticleID: 1, Action: ArticleBehaviorReply, LastSeenAt: now.Add(time.Minute)},
			{Model: gorm.Model{ID: 2}, ArticleID: 2, Action: ArticleBehaviorView, LastSeenAt: now},
		},
		[]FeedbackEvent{{EventID: "click", ArticleID: 2, EventType: models.RecommendationEventTypeClick, OccurredAt: now},
			{EventID: "read", ArticleID: 3, EventType: eventing.EventTypeRecommendationReadEnd, OccurredAt: now, ReadOutcome: &qualified}},
		map[uint]ReactionState{1: {Liked: true, StateChangedAt: now}, 4: {Liked: false, StateChangedAt: now}},
	)
	if len(result.InteractedArticleIDs) != 4 || len(result.Outcomes) != 3 {
		t.Fatalf("result=%#v", result)
	}
	if result.InteractedArticleIDs[0] != 1 || result.InteractedArticleIDs[3] != 4 {
		t.Fatalf("interaction ids=%v", result.InteractedArticleIDs)
	}
	if len(result.Outcomes[0].PositiveSignals) != 2 {
		t.Fatalf("like+reply outcome=%#v", result.Outcomes[0])
	}
	if result.Outcomes[1].PassiveSignal == nil || result.Outcomes[1].PassiveSignal.SignalType != "click" {
		t.Fatalf("click should supersede equal-time read only when later; outcome=%#v", result.Outcomes[1])
	}
}

func TestCanonicalizeNotInterestedAndLaterReplyPrecedence(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	ni := FeedbackEvent{EventID: "ni", ArticleID: 7, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: now.Add(2 * time.Minute)}
	result := CanonicalizeOutcomes([]models.ArticleBehavior{{ArticleID: 7, Action: ArticleBehaviorReply, LastSeenAt: now.Add(time.Minute)}}, []FeedbackEvent{ni}, map[uint]ReactionState{7: {Liked: true, StateChangedAt: now.Add(2 * time.Minute)}})
	if len(result.Outcomes) != 1 || result.Outcomes[0].NegativeSignal == nil || len(result.Outcomes[0].PositiveSignals) != 0 {
		t.Fatalf("NI should override equal-time positives: %#v", result)
	}
	result = CanonicalizeOutcomes([]models.ArticleBehavior{{ArticleID: 7, Action: ArticleBehaviorReply, LastSeenAt: now.Add(3 * time.Minute)}}, []FeedbackEvent{ni}, nil)
	if len(result.Outcomes) != 1 || len(result.Outcomes[0].PositiveSignals) != 1 || result.Outcomes[0].NegativeSignal != nil {
		t.Fatalf("later reply should restore positive: %#v", result)
	}
}
