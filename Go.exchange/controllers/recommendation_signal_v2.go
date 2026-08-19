package controllers

import (
	"errors"
	"strconv"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/recommendation"
)

type recommendationFeedbackEvent struct {
	EventID     string
	ArticleID   uint
	EventType   string
	OccurredAt  time.Time
	ReceivedAt  time.Time
	ReadOutcome *string
}

type recommendationFeedbackSignal struct {
	Event recommendationFeedbackEvent
}

type recommendationReactionState struct {
	Liked          bool
	StateChangedAt time.Time
}

type userArticleSignal struct {
	SignalType string
	OccurredAt time.Time
}

type userArticleOutcome struct {
	ArticleID       uint
	PositiveSignals []userArticleSignal
	NegativeSignal  *userArticleSignal
	PassiveSignal   *userArticleSignal
}

const (
	recommendationFeedbackEventTypeClick         = models.RecommendationEventTypeClick
	recommendationFeedbackEventTypeReadEnd       = models.RecommendationEventTypeReadEnd
	recommendationFeedbackEventTypeNotInterested = models.RecommendationEventTypeNotInterested
)

var loadRecommendationBehaviorSignals = func(userID uint) ([]articleBehaviorSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var views []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND action = ?", userID, ArticleBehaviorActionView).
		Order("last_seen_at DESC, id DESC").Limit(recommendationRecentViewArticleLimit).Find(&views).Error; err != nil {
		return nil, err
	}
	var replies []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND action = ?", userID, ArticleBehaviorActionReply).
		Order("last_seen_at DESC, id DESC").Limit(recommendationFeedbackArticleLimit).Find(&replies).Error; err != nil {
		return nil, err
	}
	result := make([]articleBehaviorSignal, 0, len(views)+len(replies))
	for _, behavior := range views {
		if behavior.ArticleID != 0 {
			result = append(result, articleBehaviorSignal{Behavior: behavior})
		}
	}
	for _, behavior := range replies {
		if behavior.ArticleID != 0 {
			result = append(result, articleBehaviorSignal{Behavior: behavior})
		}
	}
	return result, nil
}

var loadRecommendationFeedbackSignals = func(userID uint, lookbackStart time.Time) ([]recommendationFeedbackSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	actions := []string{
		eventing.RecommendationBehaviorActionClick,
		eventing.RecommendationBehaviorActionReadQualified,
		eventing.RecommendationBehaviorActionReadQuickBounce,
		eventing.RecommendationBehaviorActionReadNeutral,
	}
	var behaviors []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND ((action IN ? AND last_seen_at >= ?) OR action = ?)",
		userID, actions, lookbackStart, eventing.RecommendationBehaviorActionNotInterested).
		Order("last_seen_at DESC, id DESC").Find(&behaviors).Error; err != nil {
		return nil, err
	}
	result := make([]recommendationFeedbackSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID == 0 {
			continue
		}
		event := recommendationFeedbackEvent{
			EventID: strconv.FormatUint(uint64(behavior.ID), 10), ArticleID: behavior.ArticleID,
			OccurredAt: behavior.LastSeenAt, ReceivedAt: behavior.UpdatedAt,
		}
		if event.ReceivedAt.IsZero() {
			event.ReceivedAt = event.OccurredAt
		}
		switch behavior.Action {
		case eventing.RecommendationBehaviorActionClick:
			event.EventType = recommendationFeedbackEventTypeClick
		case eventing.RecommendationBehaviorActionReadQualified,
			eventing.RecommendationBehaviorActionReadQuickBounce,
			eventing.RecommendationBehaviorActionReadNeutral:
			event.EventType = recommendationFeedbackEventTypeReadEnd
			outcome := recommendationReadOutcomeNeutral
			switch behavior.Action {
			case eventing.RecommendationBehaviorActionReadQualified:
				outcome = recommendationReadOutcomeQualified
			case eventing.RecommendationBehaviorActionReadQuickBounce:
				outcome = recommendationReadOutcomeQuickBounce
			}
			event.ReadOutcome = &outcome
		case eventing.RecommendationBehaviorActionNotInterested:
			event.EventType = recommendationFeedbackEventTypeNotInterested
		default:
			continue
		}
		result = append(result, recommendationFeedbackSignal{Event: event})
	}
	return result, nil
}

var loadRecommendationReactionStates = func(userID uint) (map[uint]recommendationReactionState, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var reactions []models.ArticleReaction
	if err := global.Db.Where("user_id = ?", userID).Order("article_id ASC").Find(&reactions).Error; err != nil {
		return nil, err
	}
	states := make(map[uint]recommendationReactionState, len(reactions))
	for _, reaction := range reactions {
		if reaction.ArticleID != 0 {
			states[reaction.ArticleID] = recommendationReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
		}
	}
	return states, nil
}

func normalizeRecommendationFeedbackSignal(event recommendationFeedbackEvent) string {
	switch event.EventType {
	case recommendationFeedbackEventTypeClick:
		return "click"
	case recommendationFeedbackEventTypeNotInterested:
		return "not_interested"
	case recommendationFeedbackEventTypeReadEnd:
		if event.ReadOutcome == nil {
			return "neutral_read"
		}
		switch *event.ReadOutcome {
		case recommendationReadOutcomeQualified:
			return "qualified_read"
		case recommendationReadOutcomeQuickBounce:
			return "quick_bounce"
		default:
			return "neutral_read"
		}
	default:
		return ""
	}
}

func recommendationEventAfter(candidate, current recommendationFeedbackEvent) bool {
	if !candidate.OccurredAt.Equal(current.OccurredAt) {
		return candidate.OccurredAt.After(current.OccurredAt)
	}
	if !candidate.ReceivedAt.Equal(current.ReceivedAt) {
		return candidate.ReceivedAt.After(current.ReceivedAt)
	}
	return candidate.EventID > current.EventID
}

func setLatestRecommendationEvent(target **recommendationFeedbackEvent, candidate recommendationFeedbackEvent) {
	if *target == nil || recommendationEventAfter(candidate, **target) {
		candidateCopy := candidate
		*target = &candidateCopy
	}
}

type recommendationArticleFeedbackState struct {
	Click         *recommendationFeedbackEvent
	ReadEnd       *recommendationFeedbackEvent
	NotInterested *recommendationFeedbackEvent
}

func resolveRecommendationPassiveOutcome(state *recommendationArticleFeedbackState, view models.ArticleBehavior) (string, time.Time) {
	var click, readEnd *recommendationFeedbackEvent
	if state != nil {
		click, readEnd = state.Click, state.ReadEnd
	}
	if readEnd != nil {
		if click != nil && click.OccurredAt.After(readEnd.OccurredAt) {
			return "click", click.OccurredAt
		}
		if view.ArticleID != 0 && view.LastSeenAt.After(readEnd.OccurredAt) {
			return "view", view.LastSeenAt
		}
		return normalizeRecommendationFeedbackSignal(*readEnd), readEnd.OccurredAt
	}
	if click != nil {
		return "click", click.OccurredAt
	}
	if view.ArticleID != 0 {
		return "view", view.LastSeenAt
	}
	return "", time.Time{}
}

func canonicalizeRecommendationOutcomes(behaviors []articleBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState) []userArticleOutcome {
	behaviorRows := make([]models.ArticleBehavior, 0, len(behaviors))
	for _, item := range behaviors {
		behaviorRows = append(behaviorRows, item.Behavior)
	}
	feedbackRows := make([]recommendation.FeedbackEvent, 0, len(feedback))
	for _, item := range feedback {
		feedbackRows = append(feedbackRows, recommendation.FeedbackEvent{
			EventID: item.Event.EventID, ArticleID: item.Event.ArticleID, EventType: item.Event.EventType,
			OccurredAt: item.Event.OccurredAt, ReceivedAt: item.Event.ReceivedAt, ReadOutcome: item.Event.ReadOutcome,
		})
	}
	reactionRows := make(map[uint]recommendation.ReactionState, len(reactions))
	for articleID, reaction := range reactions {
		reactionRows[articleID] = recommendation.ReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
	}
	result := recommendation.CanonicalizeOutcomes(behaviorRows, feedbackRows, reactionRows)
	outcomes := make([]userArticleOutcome, 0, len(result.Outcomes))
	for _, item := range result.Outcomes {
		outcome := userArticleOutcome{ArticleID: item.ArticleID}
		for _, signal := range item.PositiveSignals {
			outcome.PositiveSignals = append(outcome.PositiveSignals, userArticleSignal{SignalType: signal.SignalType, OccurredAt: signal.OccurredAt})
		}
		if item.NegativeSignal != nil {
			outcome.NegativeSignal = &userArticleSignal{SignalType: item.NegativeSignal.SignalType, OccurredAt: item.NegativeSignal.OccurredAt}
		}
		if item.PassiveSignal != nil {
			outcome.PassiveSignal = &userArticleSignal{SignalType: item.PassiveSignal.SignalType, OccurredAt: item.PassiveSignal.OccurredAt}
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
