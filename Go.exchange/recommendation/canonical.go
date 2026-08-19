package recommendation

import (
	"sort"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"
)

const CanonicalOutcomeVersion = "multi_signal_capped_v2"

type UserArticleSignal struct {
	SignalType string
	OccurredAt time.Time
}

type UserArticleOutcome struct {
	ArticleID       uint
	PositiveSignals []UserArticleSignal
	NegativeSignal  *UserArticleSignal
	PassiveSignal   *UserArticleSignal
}

type CanonicalizationResult struct {
	Outcomes             []UserArticleOutcome
	InteractedArticleIDs []uint
}

type articleFeedbackState struct {
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

func resolvePassiveOutcome(state *articleFeedbackState, view *models.ArticleBehavior) (string, time.Time) {
	var click, readEnd *FeedbackEvent
	if state != nil {
		click, readEnd = state.Click, state.ReadEnd
	}
	if readEnd != nil {
		if click != nil && click.OccurredAt.After(readEnd.OccurredAt) {
			return "click", click.OccurredAt
		}
		if view != nil && view.ArticleID != 0 && view.LastSeenAt.After(readEnd.OccurredAt) {
			return "view", view.LastSeenAt
		}
		return normalizedFeedbackSignal(*readEnd), readEnd.OccurredAt
	}
	if click != nil {
		return "click", click.OccurredAt
	}
	if view != nil && view.ArticleID != 0 {
		return "view", view.LastSeenAt
	}
	return "", time.Time{}
}

// CanonicalizeOutcomes preserves the accepted V3 signal semantics while
// additionally returning the complete interaction keyset. In particular, a
// reaction-only unliked row remains an interacted article even when it has no
// positive, passive, or negative contribution.
func CanonicalizeOutcomes(behaviors []models.ArticleBehavior, feedback []FeedbackEvent, reactions map[uint]ReactionState) CanonicalizationResult {
	views := make(map[uint]models.ArticleBehavior)
	replies := make(map[uint]models.ArticleBehavior)
	feedbackByArticle := make(map[uint]*articleFeedbackState)
	articleIDs := make(map[uint]struct{})
	for _, behavior := range behaviors {
		articleID := behavior.ArticleID
		if articleID == 0 {
			continue
		}
		articleIDs[articleID] = struct{}{}
		switch behavior.Action {
		case ArticleBehaviorView:
			current, exists := views[articleID]
			if !exists || behavior.LastSeenAt.After(current.LastSeenAt) ||
				(behavior.LastSeenAt.Equal(current.LastSeenAt) && behavior.ID > current.ID) {
				views[articleID] = behavior
			}
		case ArticleBehaviorReply:
			current, exists := replies[articleID]
			if !exists || behavior.LastSeenAt.After(current.LastSeenAt) ||
				(behavior.LastSeenAt.Equal(current.LastSeenAt) && behavior.ID > current.ID) {
				replies[articleID] = behavior
			}
		}
	}
	for _, event := range feedback {
		if event.ArticleID == 0 {
			continue
		}
		articleIDs[event.ArticleID] = struct{}{}
		state := feedbackByArticle[event.ArticleID]
		if state == nil {
			state = &articleFeedbackState{}
			feedbackByArticle[event.ArticleID] = state
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
	for articleID := range reactions {
		if articleID != 0 {
			articleIDs[articleID] = struct{}{}
		}
	}

	ids := make([]uint, 0, len(articleIDs))
	for articleID := range articleIDs {
		ids = append(ids, articleID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	outcomes := make([]UserArticleOutcome, 0, len(ids))
	for _, articleID := range ids {
		state := feedbackByArticle[articleID]
		reaction, hasReaction := reactions[articleID]
		var notInterested *FeedbackEvent
		if state != nil {
			notInterested = state.NotInterested
		}
		positive := make([]UserArticleSignal, 0, 2)
		if hasReaction && reaction.Liked && (notInterested == nil || reaction.StateChangedAt.After(notInterested.OccurredAt)) {
			positive = append(positive, UserArticleSignal{SignalType: "like", OccurredAt: reaction.StateChangedAt})
		}
		if reply, ok := replies[articleID]; ok && (notInterested == nil || reply.LastSeenAt.After(notInterested.OccurredAt)) {
			positive = append(positive, UserArticleSignal{SignalType: "reply", OccurredAt: reply.LastSeenAt})
		}
		passiveType, passiveAt := resolvePassiveOutcome(state, behaviorPointer(views, articleID))
		outcome := UserArticleOutcome{ArticleID: articleID, PositiveSignals: positive}
		if passiveType != "" {
			outcome.PassiveSignal = &UserArticleSignal{SignalType: passiveType, OccurredAt: passiveAt}
		}
		switch {
		case len(positive) > 0:
			// Positive signals override negative state, preserving the current
			// later-like/later-reply restore rule.
		case notInterested != nil:
			outcome.NegativeSignal = &UserArticleSignal{SignalType: "not_interested", OccurredAt: notInterested.OccurredAt}
		case passiveType == "quick_bounce":
			outcome.NegativeSignal = &UserArticleSignal{SignalType: passiveType, OccurredAt: passiveAt}
		}
		if len(outcome.PositiveSignals) > 0 || outcome.NegativeSignal != nil || outcome.PassiveSignal != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	return CanonicalizationResult{Outcomes: outcomes, InteractedArticleIDs: ids}
}

func behaviorPointer(values map[uint]models.ArticleBehavior, articleID uint) *models.ArticleBehavior {
	value, ok := values[articleID]
	if !ok {
		return nil
	}
	return &value
}
