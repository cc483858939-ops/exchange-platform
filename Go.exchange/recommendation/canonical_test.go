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
		[]models.PostBehavior{
			{Model: gorm.Model{ID: 1}, PostID: 1, Action: PostBehaviorReply, LastSeenAt: now.Add(time.Minute)},
			{Model: gorm.Model{ID: 2}, PostID: 2, Action: PostBehaviorView, LastSeenAt: now},
		},
		[]FeedbackEvent{{EventID: "click", PostID: 2, EventType: models.RecommendationEventTypeClick, OccurredAt: now},
			{EventID: "read", PostID: 3, EventType: eventing.EventTypeRecommendationReadEnd, OccurredAt: now, ReadOutcome: &qualified}},
		map[uint]ReactionState{1: {Liked: true, StateChangedAt: now}, 4: {Liked: false, StateChangedAt: now}},
	)
	if len(result.InteractedPostIDs) != 4 || len(result.Outcomes) != 3 {
		t.Fatalf("result=%#v", result)
	}
	if result.InteractedPostIDs[0] != 1 || result.InteractedPostIDs[3] != 4 {
		t.Fatalf("interaction ids=%v", result.InteractedPostIDs)
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
	ni := FeedbackEvent{EventID: "ni", PostID: 7, EventType: models.RecommendationEventTypeNotInterested, OccurredAt: now.Add(2 * time.Minute)}
	result := CanonicalizeOutcomes([]models.PostBehavior{{PostID: 7, Action: PostBehaviorReply, LastSeenAt: now.Add(time.Minute)}}, []FeedbackEvent{ni}, map[uint]ReactionState{7: {Liked: true, StateChangedAt: now.Add(2 * time.Minute)}})
	if len(result.Outcomes) != 1 || result.Outcomes[0].NegativeSignal == nil || len(result.Outcomes[0].PositiveSignals) != 0 {
		t.Fatalf("NI should override equal-time positives: %#v", result)
	}
	result = CanonicalizeOutcomes([]models.PostBehavior{{PostID: 7, Action: PostBehaviorReply, LastSeenAt: now.Add(3 * time.Minute)}}, []FeedbackEvent{ni}, nil)
	if len(result.Outcomes) != 1 || len(result.Outcomes[0].PositiveSignals) != 1 || result.Outcomes[0].NegativeSignal != nil {
		t.Fatalf("later reply should restore positive: %#v", result)
	}
}
