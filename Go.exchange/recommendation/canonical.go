package recommendation

import (
	"sort"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"
)

const CanonicalOutcomeVersion = "multi_signal_capped_v2"

type UserPostSignal struct {
	SignalType string
	OccurredAt time.Time
}

type UserPostOutcome struct {
	PostID       uint
	PositiveSignals []UserPostSignal
	NegativeSignal  *UserPostSignal
	PassiveSignal   *UserPostSignal
}

type CanonicalizationResult struct {
	Outcomes             []UserPostOutcome
	InteractedPostIDs []uint
}

type postFeedbackState struct {
	Click         *FeedbackEvent
	ReadEnd       *FeedbackEvent
	NotInterested *FeedbackEvent
}

func feedbackEventAfter(candidate, current FeedbackEvent) bool {
	if !candidate.OccurredAt.Equal(current.OccurredAt) {
		return candidate.OccurredAt.After(current.OccurredAt)
	}
	if !candidate.ReceivedAt.Equal(current.ReceivedAt) {
		return candidate.ReceivedAt.After(current.ReceivedAt)
	}
	return candidate.EventID > current.EventID
}

func setLatestFeedback(target **FeedbackEvent, candidate FeedbackEvent) {
	if *target == nil || feedbackEventAfter(candidate, **target) {
		copy := candidate
		*target = &copy
	}
}

func normalizedFeedbackSignal(event FeedbackEvent) string {
	switch event.EventType {
	case eventing.EventTypeRecommendationClick, models.RecommendationEventTypeClick:
		return "click"
	case eventing.EventTypeRecommendationNotInterested, models.RecommendationEventTypeNotInterested:
		return "not_interested"
	case eventing.EventTypeRecommendationReadEnd, models.RecommendationEventTypeReadEnd:
		if event.ReadOutcome == nil {
			return "neutral_read"
		}
		switch *event.ReadOutcome {
		case "qualified":
			return "qualified_read"
		case "quick_bounce":
			return "quick_bounce"
		default:
			return "neutral_read"
		}
	default:
		return ""
	}
}

func resolvePassiveOutcome(state *postFeedbackState, view *models.PostBehavior) (string, time.Time) {
	var click, readEnd *FeedbackEvent
	if state != nil {
		click, readEnd = state.Click, state.ReadEnd
	}
	if readEnd != nil {
		if click != nil && click.OccurredAt.After(readEnd.OccurredAt) {
			return "click", click.OccurredAt
		}
		if view != nil && view.PostID != 0 && view.LastSeenAt.After(readEnd.OccurredAt) {
			return "view", view.LastSeenAt
		}
		return normalizedFeedbackSignal(*readEnd), readEnd.OccurredAt
	}
	if click != nil {
		return "click", click.OccurredAt
	}
	if view != nil && view.PostID != 0 {
		return "view", view.LastSeenAt
	}
	return "", time.Time{}
}

// CanonicalizeOutcomes preserves the accepted V3 signal semantics while
// additionally returning the complete interaction keyset. In particular, a
// reaction-only unliked row remains an interacted post even when it has no
// positive, passive, or negative contribution.
func CanonicalizeOutcomes(behaviors []models.PostBehavior, feedback []FeedbackEvent, reactions map[uint]ReactionState) CanonicalizationResult {
	views := make(map[uint]models.PostBehavior)
	replies := make(map[uint]models.PostBehavior)
	feedbackByPost := make(map[uint]*postFeedbackState)
	postIDs := make(map[uint]struct{})
	for _, behavior := range behaviors {
		postID := behavior.PostID
		if postID == 0 {
			continue
		}
		postIDs[postID] = struct{}{}
		switch behavior.Action {
		case PostBehaviorView:
			current, exists := views[postID]
			if !exists || behavior.LastSeenAt.After(current.LastSeenAt) ||
				(behavior.LastSeenAt.Equal(current.LastSeenAt) && behavior.ID > current.ID) {
				views[postID] = behavior
			}
		case PostBehaviorReply:
			current, exists := replies[postID]
			if !exists || behavior.LastSeenAt.After(current.LastSeenAt) ||
				(behavior.LastSeenAt.Equal(current.LastSeenAt) && behavior.ID > current.ID) {
				replies[postID] = behavior
			}
		}
	}
	for _, event := range feedback {
		if event.PostID == 0 {
			continue
		}
		postIDs[event.PostID] = struct{}{}
		state := feedbackByPost[event.PostID]
		if state == nil {
			state = &postFeedbackState{}
			feedbackByPost[event.PostID] = state
		}
		switch event.EventType {
		case eventing.EventTypeRecommendationClick, models.RecommendationEventTypeClick:
			setLatestFeedback(&state.Click, event)
		case eventing.EventTypeRecommendationReadEnd, models.RecommendationEventTypeReadEnd:
			setLatestFeedback(&state.ReadEnd, event)
		case eventing.EventTypeRecommendationNotInterested, models.RecommendationEventTypeNotInterested:
			setLatestFeedback(&state.NotInterested, event)
		}
	}
	for postID := range reactions {
		if postID != 0 {
			postIDs[postID] = struct{}{}
		}
	}

	ids := make([]uint, 0, len(postIDs))
	for postID := range postIDs {
		ids = append(ids, postID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	outcomes := make([]UserPostOutcome, 0, len(ids))
	for _, postID := range ids {
		state := feedbackByPost[postID]
		reaction, hasReaction := reactions[postID]
		var notInterested *FeedbackEvent
		if state != nil {
			notInterested = state.NotInterested
		}
		positive := make([]UserPostSignal, 0, 2)
		if hasReaction && reaction.Liked && (notInterested == nil || reaction.StateChangedAt.After(notInterested.OccurredAt)) {
			positive = append(positive, UserPostSignal{SignalType: "like", OccurredAt: reaction.StateChangedAt})
		}
		if reply, ok := replies[postID]; ok && (notInterested == nil || reply.LastSeenAt.After(notInterested.OccurredAt)) {
			positive = append(positive, UserPostSignal{SignalType: "reply", OccurredAt: reply.LastSeenAt})
		}
		passiveType, passiveAt := resolvePassiveOutcome(state, behaviorPointer(views, postID))
		outcome := UserPostOutcome{PostID: postID, PositiveSignals: positive}
		if passiveType != "" {
			outcome.PassiveSignal = &UserPostSignal{SignalType: passiveType, OccurredAt: passiveAt}
		}
		switch {
		case len(positive) > 0:
			// Positive signals override negative state, preserving the current
			// later-like/later-reply restore rule.
		case notInterested != nil:
			outcome.NegativeSignal = &UserPostSignal{SignalType: "not_interested", OccurredAt: notInterested.OccurredAt}
		case passiveType == "quick_bounce":
			outcome.NegativeSignal = &UserPostSignal{SignalType: passiveType, OccurredAt: passiveAt}
		}
		if len(outcome.PositiveSignals) > 0 || outcome.NegativeSignal != nil || outcome.PassiveSignal != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	return CanonicalizationResult{Outcomes: outcomes, InteractedPostIDs: ids}
}

func behaviorPointer(values map[uint]models.PostBehavior, postID uint) *models.PostBehavior {
	value, ok := values[postID]
	if !ok {
		return nil
	}
	return &value
}
